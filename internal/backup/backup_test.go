package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

func openStore(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "subglance.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// clock is a settable time source, advanced by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newBackups(t *testing.T, db *store.DB, endpoint string, keep int, clk *clock) *Backups {
	t.Helper()
	target, err := ParseTarget("s3://bucket/nightly", endpoint, "test-region")
	if err != nil {
		t.Fatalf("ParseTarget: %v", err)
	}
	b, err := New(Options{
		DB: db, Target: target,
		AccessKeyID: "test-key", SecretAccessKey: "test-secret",
		Keep: keep, StagingDir: t.TempDir(), Now: clk.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestRunOnceUploadsACompressedSnapshotThatRestores(t *testing.T) {
	db := openStore(t)
	if _, err := db.Writer.Exec(`CREATE TABLE marker (v TEXT); INSERT INTO marker VALUES ('still here')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fake, srv := newFakeS3(t, "bucket")
	clk := &clock{t: time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 3, clk)

	res, err := b.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if want := "subglance-20260925T030000Z.db.gz"; res.Object != want {
		t.Errorf("object = %q, want %q", res.Object, want)
	}
	if got := fake.keys(); len(got) != 1 || got[0] != "nightly/"+res.Object {
		t.Fatalf("bucket holds %v, want one object under the prefix", got)
	}

	// Download it the way restore does, and read the row back.
	dest := filepath.Join(t.TempDir(), "restored.db")
	if err := Download(context.Background(), b.client, b.target, res.Object, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	restored, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer func() { _ = restored.Close() }()
	var v string
	if err := restored.QueryRow(`SELECT v FROM marker`).Scan(&v); err != nil || v != "still here" {
		t.Fatalf("restored row = %q, %v; want the row written before the backup", v, err)
	}

	st := b.Status()
	if st.LastSuccess.IsZero() || st.LastObject != res.Object || st.LastSize != res.Size || st.LastError != "" {
		t.Errorf("status after success = %+v", st)
	}

	// The staging directory is left empty: a backup that leaves a copy of
	// the database behind on every run fills the disk it was meant to save.
	entries, _ := os.ReadDir(b.staging)
	if len(entries) != 0 {
		t.Errorf("staging dir still holds %d files", len(entries))
	}
}

func TestRunOnceKeepsExactlyKeepBackups(t *testing.T) {
	db := openStore(t)
	fake, srv := newFakeS3(t, "bucket")
	// Something the operator keeps under the same prefix, which pruning
	// must never touch.
	fake.objects["nightly/README.txt"] = []byte("mine")
	fake.objects["nightly/subglance-notes.txt"] = []byte("mine too, despite the name")
	fake.pageSize = 2 // several listing pages, so continuation is exercised

	clk := &clock{t: time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 3, clk)

	for i := 0; i < 5; i++ {
		if _, err := b.RunOnce(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		clk.Add(24 * time.Hour)
	}

	want := []string{
		"nightly/README.txt",
		"nightly/subglance-20260903T030000Z.db.gz",
		"nightly/subglance-20260904T030000Z.db.gz",
		"nightly/subglance-20260905T030000Z.db.gz",
		"nightly/subglance-notes.txt",
	}
	if got := fake.keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("bucket after 5 runs with keep=3:\n got %v\nwant %v", got, want)
	}
}

func TestFailedUploadLeavesEarlierBackupsAlone(t *testing.T) {
	db := openStore(t)
	fake, srv := newFakeS3(t, "bucket")
	clk := &clock{t: time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 1, clk)

	if _, err := b.RunOnce(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := fake.keys()

	clk.Add(24 * time.Hour)
	fake.failPut = 1
	_, err := b.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("RunOnce with a failing upload = %v, want the service's error", err)
	}
	if got := fake.keys(); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("a failed upload changed the bucket: before %v, after %v", before, got)
	}
	st := b.Status()
	if st.LastError == "" || st.Failures != 1 || st.LastObject != "subglance-20260901T030000Z.db.gz" {
		t.Errorf("status after a failure = %+v; want the error, one failure and the earlier success kept", st)
	}
}

func TestPruneFailureStillCountsTheUpload(t *testing.T) {
	db := openStore(t)
	fake, srv := newFakeS3(t, "bucket")
	clk := &clock{t: time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 1, clk)
	if _, err := b.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk.Add(24 * time.Hour)
	fake.failDelete = 1

	res, err := b.RunOnce(context.Background())
	if !IsPruneError(err) {
		t.Fatalf("err = %v, want a prune error", err)
	}
	st := b.Status()
	if st.LastObject != res.Object || st.LastError == "" {
		t.Errorf("status = %+v; want the new upload recorded and the prune error shown", st)
	}
}

func TestRunOnceRefusesToOverlap(t *testing.T) {
	b := &Backups{}
	b.running.Store(true)
	if _, err := b.RunOnce(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("RunOnce while running = %v, want ErrBusy", err)
	}
}

func TestFirstRunFollowsTheNewestBackup(t *testing.T) {
	db := openStore(t)
	fake, srv := newFakeS3(t, "bucket")
	clk := &clock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 3, clk)

	if got := b.firstRun(context.Background()); !got.Equal(clk.Now()) {
		t.Errorf("empty bucket: first run at %v, want now", got)
	}

	fake.objects["nightly/subglance-20260925T030000Z.db.gz"] = []byte("x")
	want := time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC)
	if got := b.firstRun(context.Background()); !got.Equal(want) {
		t.Errorf("after a restart: first run at %v, want one interval after the newest backup, %v", got, want)
	}
}

func TestAlertOncePerDayOfFailure(t *testing.T) {
	var a AlertGate
	t0 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	if !a.ShouldAlert(t0) {
		t.Error("the first failure must alert")
	}
	if a.ShouldAlert(t0.Add(time.Hour)) {
		t.Error("the hourly retry of the same failure must not alert again")
	}
	if !a.ShouldAlert(t0.Add(24 * time.Hour)) {
		t.Error("a failure still going a day later must alert again")
	}
	a.Forget()
	if !a.ShouldAlert(t0.Add(25 * time.Hour)) {
		t.Error("after a notice that could not be sent, the next failure must try again")
	}
}

func TestParseTarget(t *testing.T) {
	good := []struct{ raw, bucket, prefix string }{
		{"s3://b", "b", ""},
		{"s3://b/", "b", ""},
		{"s3://b/backups", "b", "backups/"},
		{"s3://b/a/b/", "b", "a/b/"},
	}
	for _, c := range good {
		got, err := ParseTarget(c.raw, "", "")
		if err != nil || got.Bucket != c.bucket || got.Prefix != c.prefix || got.Region != "us-east-1" {
			t.Errorf("ParseTarget(%q) = %+v, %v", c.raw, got, err)
		}
	}
	bad := []struct{ raw, endpoint string }{
		{"https://b/x", ""},
		{"s3:///x", ""},
		{"s3://b/x?versionId=1", ""},
		{"s3://b", "s3.example.com"},
		{"s3://b", "https://s3.example.com/bucket"},
	}
	for _, c := range bad {
		if _, err := ParseTarget(c.raw, c.endpoint, ""); err == nil {
			t.Errorf("ParseTarget(%q, %q) accepted", c.raw, c.endpoint)
		}
	}
}

func TestObjectURLStyles(t *testing.T) {
	aws := &client{target: Target{Bucket: "b", Region: "eu-west-1"}}
	if got := aws.objectURL("p/x y.gz").String(); got != "https://b.s3.eu-west-1.amazonaws.com/p/x%20y.gz" {
		t.Errorf("AWS URL = %s", got)
	}
	dotted := &client{target: Target{Bucket: "b.example", Region: "eu-west-1"}}
	if got := dotted.objectURL("x").String(); got != "https://s3.eu-west-1.amazonaws.com/b.example/x" {
		t.Errorf("dotted bucket URL = %s, want path style", got)
	}
	custom := &client{target: Target{Bucket: "b", Endpoint: "https://minio.example:9000", Region: "x"}}
	if got := custom.objectURL("x").String(); got != "https://minio.example:9000/b/x" {
		t.Errorf("custom endpoint URL = %s, want path style", got)
	}
}

// Run on a bucket that refuses uploads: the first run happens straight away,
// the failure reaches onFailure, and cancelling stops the loop.
func TestRunReportsAFailedBackup(t *testing.T) {
	db := openStore(t)
	fake, srv := newFakeS3(t, "bucket")
	fake.failPut = 100
	clk := &clock{t: time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)}
	b := newBackups(t, db, srv.URL, 3, clk)

	failures := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(ctx, func(err error) { failures <- err })
	}()

	select {
	case err := <-failures:
		if !strings.Contains(err.Error(), "upload") {
			t.Errorf("failure = %v, want the upload error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a failed backup never reached onFailure")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
	if st := b.Status(); st.Failures != 1 || st.LastError == "" {
		t.Errorf("status = %+v", st)
	}
}
