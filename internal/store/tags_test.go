package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNormaliseTags(t *testing.T) {
	cases := []struct {
		name    string
		in      map[string]string
		want    map[string]string
		wantErr string
	}{
		{name: "empty stays nil", in: map[string]string{}},
		{
			name: "key is trimmed and lowercased, value keeps its case",
			in:   map[string]string{"  Customer ": "  Acme B.V. "},
			want: map[string]string{"customer": "Acme B.V."},
		},
		{
			name: "value may contain the separator",
			in:   map[string]string{"docs": "https://example.com:8443/a"},
			want: map[string]string{"docs": "https://example.com:8443/a"},
		},
		{
			name:    "a bare tag is rejected rather than turned into tag:value",
			in:      map[string]string{"env": "   "},
			wantErr: "must have a value",
		},
		{
			name:    "empty key is rejected",
			in:      map[string]string{"  ": "prod"},
			wantErr: "must not be empty",
		},
		{
			name:    "keys that differ only in case are a mistake, not a last-wins race",
			in:      map[string]string{"Env": "prod", "env": "staging"},
			wantErr: "appears twice",
		},
		{
			name:    "a key with a space cannot be a column header or query parameter",
			in:      map[string]string{"my env": "prod"},
			wantErr: "must be lowercase letters",
		},
		{
			name:    "a key may not start with a dash",
			in:      map[string]string{"-env": "prod"},
			wantErr: "must be lowercase letters",
		},
		{
			name:    "over-long key",
			in:      map[string]string{strings.Repeat("k", maxTagKeyLen+1): "v"},
			wantErr: "longer than",
		},
		{
			name:    "over-long value",
			in:      map[string]string{"env": strings.Repeat("v", maxTagValueLen+1)},
			wantErr: "longer than",
		},
		{
			name:    "a newline in a value would break every log line and table cell",
			in:      map[string]string{"note": "a\nb"},
			wantErr: "line breaks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormaliseTags(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("NormaliseTags(%v) = %v, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormaliseTags(%v): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormaliseTags(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormaliseTagsRejectsTooMany(t *testing.T) {
	in := make(map[string]string, MaxTagsPerMonitor+1)
	for i := 0; i <= MaxTagsPerMonitor; i++ {
		in["k"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	if _, err := NormaliseTags(in); err == nil {
		t.Fatalf("NormaliseTags accepted %d tags, want a limit of %d", len(in), MaxTagsPerMonitor)
	}
}

// NormaliseTags must not mutate its argument: the API hands it a map decoded
// straight from the request body and reports that body back in errors.
func TestNormaliseTagsDoesNotMutateInput(t *testing.T) {
	in := map[string]string{" Env ": " prod "}
	if _, err := NormaliseTags(in); err != nil {
		t.Fatalf("NormaliseTags: %v", err)
	}
	if _, ok := in[" Env "]; !ok || len(in) != 1 {
		t.Fatalf("input was mutated: %v", in)
	}
}

func TestTagsRoundTripThroughCreateAndGet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "tagged", Type: "http", Target: "https://a.example", Enabled: true,
		Tags: map[string]string{"env": "prod", "customer": "Acme"},
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	want := map[string]string{"env": "prod", "customer": "Acme"}
	if !reflect.DeepEqual(got.Tags, want) {
		t.Fatalf("GetMonitor tags = %v, want %v", got.Tags, want)
	}

	list, err := db.ListMonitors(ctx)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(list) != 1 || !reflect.DeepEqual(list[0].Tags, want) {
		t.Fatalf("ListMonitors tags = %v, want %v", list, want)
	}
}

// Updating replaces the whole set, so a tag can be removed. Merging would make
// removal impossible through the API's single `tags` field.
func TestUpdateMonitorReplacesTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "t", Type: "http", Target: "https://a.example", Enabled: true,
		Tags: map[string]string{"env": "prod", "customer": "Acme"},
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	m.Tags = map[string]string{"env": "staging"}
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}

	got, err := db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if !reflect.DeepEqual(got.Tags, map[string]string{"env": "staging"}) {
		t.Fatalf("tags after update = %v, want only env=staging", got.Tags)
	}

	m.Tags = nil
	if _, err := db.UpdateMonitor(ctx, m); err != nil {
		t.Fatalf("UpdateMonitor clearing tags: %v", err)
	}
	got, err = db.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("tags after clearing = %v, want none", got.Tags)
	}
}

// Deleting a monitor must take its tags with it; ON DELETE CASCADE only does
// that when foreign keys are actually enabled on the connection.
func TestDeleteMonitorCascadesToTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "t", Type: "http", Target: "https://a.example", Enabled: true,
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if err := db.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	var n int
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM monitor_tags WHERE monitor_id = ?", m.ID).Scan(&n); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d tag rows survived the monitor", n)
	}
}

// One value per key per monitor is a schema guarantee, not a convention.
func TestMonitorTagsRejectASecondValueForOneKey(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m, err := db.CreateMonitor(ctx, Monitor{
		Name: "t", Type: "http", Target: "https://a.example", Enabled: true,
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	_, err = db.Writer.ExecContext(ctx,
		"INSERT INTO monitor_tags (monitor_id, key, value) VALUES (?, ?, ?)", m.ID, "env", "staging")
	if err == nil {
		t.Fatal("a second value for one key was accepted; the primary key is not enforcing it")
	}
}
