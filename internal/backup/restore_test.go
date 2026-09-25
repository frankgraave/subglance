package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func restoreOpts(t *testing.T, endpoint, dbPath string) RestoreOptions {
	t.Helper()
	target, err := ParseTarget("s3://bucket/nightly", endpoint, "test-region")
	if err != nil {
		t.Fatal(err)
	}
	return RestoreOptions{
		Target: target, AccessKeyID: "test-key", SecretAccessKey: "test-secret",
		DBPath: dbPath,
		Now:    func() time.Time { return time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC) },
	}
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow(`SELECT v FROM marker`).Scan(&v); err != nil {
		t.Fatalf("read marker from %s: %v", path, err)
	}
	return v
}

// The done-when line in one test: backup plus restore gives back the same
// data, and the database it replaced is kept rather than deleted.
func TestBackupThenRestoreGivesBackTheSameData(t *testing.T) {
	db := openStore(t)
	if _, err := db.Writer.Exec(`CREATE TABLE marker (v TEXT); INSERT INTO marker VALUES ('before the disk died')`); err != nil {
		t.Fatal(err)
	}
	_, srv := newFakeS3(t, "bucket")
	clk := &clock{t: time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 3, clk)
	if _, err := b.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A different, newer database sits where the restore goes, with a
	// leftover -wal beside it.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subglance.db")
	current, _ := sql.Open("sqlite", dbPath)
	if _, err := current.Exec(`CREATE TABLE marker (v TEXT); INSERT INTO marker VALUES ('the wrong data')`); err != nil {
		t.Fatal(err)
	}
	_ = current.Close()
	if err := os.WriteFile(dbPath+"-wal", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Restore(context.Background(), restoreOpts(t, srv.URL, dbPath))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readMarker(t, dbPath); got != "before the disk died" {
		t.Errorf("restored database says %q", got)
	}
	if res.Object != "subglance-20260925T030000Z.db.gz" {
		t.Errorf("restored %q, want the newest backup", res.Object)
	}
	if len(res.SetAside) != 2 {
		t.Fatalf("set aside %v, want the old database and its -wal", res.SetAside)
	}
	if got := readMarker(t, res.SetAside[0]); got != "the wrong data" {
		t.Errorf("the database that was replaced was not kept: %q", got)
	}
	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Errorf("the old -wal is still next to the restored database, where SQLite would replay it")
	}
}

func TestRestorePicksANamedBackup(t *testing.T) {
	db := openStore(t)
	if _, err := db.Writer.Exec(`CREATE TABLE marker (v TEXT); INSERT INTO marker VALUES ('first')`); err != nil {
		t.Fatal(err)
	}
	_, srv := newFakeS3(t, "bucket")
	clk := &clock{t: time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 3, clk)
	if _, err := b.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec(`UPDATE marker SET v = 'second'`); err != nil {
		t.Fatal(err)
	}
	clk.Add(24 * time.Hour)
	if _, err := b.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "subglance.db")
	opts := restoreOpts(t, srv.URL, dbPath)
	opts.Name = "subglance-20260924T030000Z.db.gz"
	if _, err := Restore(context.Background(), opts); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readMarker(t, dbPath); got != "first" {
		t.Errorf("restored %q, want the named, older backup", got)
	}

	opts.Name = "subglance-20200101T000000Z.db.gz"
	if _, err := Restore(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "the newest is") {
		t.Errorf("unknown name: err = %v, want one that names the newest backup", err)
	}
}

// A backup that is not a database must leave the current one untouched.
func TestRestoreRefusesACorruptBackupAndMovesNothing(t *testing.T) {
	fake, srv := newFakeS3(t, "bucket")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(bytes.Repeat([]byte("not sqlite "), 1000))
	_ = zw.Close()
	fake.objects["nightly/subglance-20260925T030000Z.db.gz"] = gz.Bytes()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subglance.db")
	current, _ := sql.Open("sqlite", dbPath)
	if _, err := current.Exec(`CREATE TABLE marker (v TEXT); INSERT INTO marker VALUES ('keep me')`); err != nil {
		t.Fatal(err)
	}
	_ = current.Close()

	if _, err := Restore(context.Background(), restoreOpts(t, srv.URL, dbPath)); err == nil {
		t.Fatal("Restore accepted a backup that is not a database")
	}
	if got := readMarker(t, dbPath); got != "keep me" {
		t.Errorf("current database changed to %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only the untouched database", names)
	}
}

func TestRestoreWithAnEmptyBucket(t *testing.T) {
	_, srv := newFakeS3(t, "bucket")
	_, err := Restore(context.Background(), restoreOpts(t, srv.URL, filepath.Join(t.TempDir(), "subglance.db")))
	if err == nil || !strings.Contains(err.Error(), "no backups found") {
		t.Fatalf("err = %v, want no backups found", err)
	}
}
