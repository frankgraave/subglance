package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestTagTransformReadsQueuedCurrentTagsAndPreservesConcurrentConfig(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	before, err := db.CreateMonitor(ctx, Monitor{Name: "before", Type: "http", Target: "https://example.com", Tags: map[string]string{"evn": "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	op := TagOperation{Action: "rename_key", Key: "evn", NewKey: "env"}
	_, etag, err := db.TransformTags(ctx, op, "", true)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the only writer while the bulk operation starts. Its preview is older
	// than these writes; it must read tags only after acquiring its transaction.
	tx, err := db.Writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	done := make(chan error, 1)
	go func() { _, _, err := db.TransformTags(ctx, op, etag, false); done <- err }()
	headers, _ := json.Marshal(map[string]string{"X-Concurrent": "keep"})
	_, err = tx.ExecContext(ctx, `UPDATE monitors SET name=?,headers_json=?,repeat_after_s=?,updated_at=updated_at+1 WHERE id=?`, "concurrent name", string(headers), 1800, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceTags(ctx, tx, before.ID, map[string]string{"evn": "prod", "team": "concurrent"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued tag transform did not finish")
	}
	after, err := db.GetMonitor(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "concurrent name" || after.Headers["X-Concurrent"] != "keep" || after.RepeatAfterS != 1800 {
		t.Fatalf("lost concurrent config: %+v", after)
	}
	if !reflect.DeepEqual(after.Tags, map[string]string{"env": "prod", "team": "concurrent"}) {
		t.Fatalf("lost concurrent tags: %v", after.Tags)
	}
	if after.UpdatedAt.Unix() < before.UpdatedAt.Unix()+2 {
		t.Fatal("both writes must advance the monitor revision")
	}
	if _, err := db.UpdateMonitorIfUnchanged(ctx, before, []time.Time{before.UpdatedAt}); err != ErrVersionConflict {
		t.Fatalf("stale update = %v, want conflict", err)
	}
}
