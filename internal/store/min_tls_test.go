package store

import (
	"crypto/tls"
	"testing"
)

// The column is nullable because "no opinion" and "explicitly TLS 1.2" are
// different settings that happen to dial the same way today. These tests pin
// the difference, since nothing observable would notice it disappearing until
// the product's default moved.

func TestMonitorMinTLSVersionRoundTrips(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	for _, version := range []uint16{
		tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13,
	} {
		created, err := db.CreateMonitor(ctx, Monitor{
			Name: "appliance", Type: "http", Target: "https://appliance.example.com",
			Enabled: true, MinTLSVersion: version,
		})
		if err != nil {
			t.Fatalf("CreateMonitor(%d): %v", version, err)
		}
		got, err := db.GetMonitor(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetMonitor: %v", err)
		}
		if got.MinTLSVersion != version {
			t.Errorf("MinTLSVersion = %d, want %d", got.MinTLSVersion, version)
		}
	}
}

func TestMonitorWithoutMinTLSVersionStoresNull(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	created, err := db.CreateMonitor(ctx, Monitor{
		Name: "plain", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// NULL specifically, not 0. Both read back as "no opinion" in Go, but
	// only NULL says so to anyone querying the table, and a backfilled 771
	// would be indistinguishable from a deliberate choice.
	var isNull bool
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT min_tls_version IS NULL FROM monitors WHERE id = ?", created.ID,
	).Scan(&isNull); err != nil {
		t.Fatalf("query column: %v", err)
	}
	if !isNull {
		t.Error("a monitor that named no TLS floor stored a value rather than NULL")
	}

	got, err := db.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.MinTLSVersion != 0 {
		t.Errorf("MinTLSVersion = %d, want 0", got.MinTLSVersion)
	}
}

func TestUpdateMonitorChangesAndClearsMinTLSVersion(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	created, err := db.CreateMonitor(ctx, Monitor{
		Name: "appliance", Type: "http", Target: "https://appliance.example.com",
		Enabled: true, MinTLSVersion: tls.VersionTLS10,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	created.MinTLSVersion = tls.VersionTLS13
	if _, err := db.UpdateMonitor(ctx, created); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	got, err := db.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.MinTLSVersion != tls.VersionTLS13 {
		t.Fatalf("MinTLSVersion = %d, want %d", got.MinTLSVersion, tls.VersionTLS13)
	}

	// And back to nothing, which an update has to be able to write: a
	// column that can only ever be set is a setting with no undo.
	got.MinTLSVersion = 0
	if _, err := db.UpdateMonitor(ctx, got); err != nil {
		t.Fatalf("UpdateMonitor clearing: %v", err)
	}
	got, err = db.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.MinTLSVersion != 0 {
		t.Errorf("MinTLSVersion = %d after clearing, want 0", got.MinTLSVersion)
	}
}

// The CHECK names four constants rather than a range, so that the set of
// versions this product offers stays a decision. A value outside it must be
// refused by the schema, not quietly stored for a checker that cannot dial it.
func TestMonitorRejectsAnUndialableTLSVersion(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	created, err := db.CreateMonitor(ctx, Monitor{
		Name: "plain", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// 768 is SSL 3.0, which crypto/tls will not negotiate at all.
	if _, err := db.Writer.ExecContext(ctx,
		"UPDATE monitors SET min_tls_version = 768 WHERE id = ?", created.ID,
	); err == nil {
		t.Error("the column accepted SSL 3.0, which no check could ever complete")
	}
}
