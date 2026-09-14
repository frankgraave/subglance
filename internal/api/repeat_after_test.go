package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Reminders are on by default. Off by default would leave the acknowledge
// button decorative for everyone who never finds the setting, which is the
// gap this field exists to close.
func TestCreateMonitorDefaultsToRemindersOn(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"minimal","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RepeatAfterS != defaultRepeatAfterS {
		t.Errorf("response repeat_after_s = %d, want %d", got.RepeatAfterS, defaultRepeatAfterS)
	}

	stored, err := db.GetMonitor(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.RepeatAfterS != defaultRepeatAfterS {
		t.Errorf("stored repeat_after_s = %d, want %d — a bare creation does not remind",
			stored.RepeatAfterS, defaultRepeatAfterS)
	}
}

// 0 has to survive the round trip. If it were treated as "unset" and replaced
// by the default, a user would be told they had switched reminders off while
// still getting them — the same trap ssl_warn_days rejects 0 to avoid.
func TestCreateMonitorKeepsExplicitZeroRepeat(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"quiet","type":"http","target":"https://example.com","repeat_after_s":0}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RepeatAfterS != 0 {
		t.Errorf("response repeat_after_s = %d, want 0", got.RepeatAfterS)
	}

	stored, err := db.GetMonitor(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.RepeatAfterS != 0 {
		t.Errorf("stored repeat_after_s = %d, want 0 — an explicit off was overwritten "+
			"by the default", stored.RepeatAfterS)
	}
}

func TestRepeatAfterRangeIsEnforced(t *testing.T) {
	srv, _ := testServerWithDB(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"below the floor", `{"name":"a","type":"http","target":"https://e.com","repeat_after_s":59}`, http.StatusBadRequest},
		{"negative", `{"name":"a","type":"http","target":"https://e.com","repeat_after_s":-1}`, http.StatusBadRequest},
		{"above a day", `{"name":"a","type":"http","target":"https://e.com","repeat_after_s":86401}`, http.StatusBadRequest},
		{"at the floor", `{"name":"a","type":"http","target":"https://e.com","repeat_after_s":60}`, http.StatusCreated},
		{"a full day", `{"name":"a","type":"http","target":"https://e.com","repeat_after_s":86400}`, http.StatusCreated},
		{"off", `{"name":"a","type":"http","target":"https://e.com","repeat_after_s":0}`, http.StatusCreated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(t, srv, "/api/v1/monitors", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusBadRequest {
				return
			}
			// The error has to name the field, or a form cannot show it
			// against the input that caused it.
			var problem struct {
				Field string `json:"field"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if problem.Field != "repeat_after_s" {
				t.Errorf("field = %q, want repeat_after_s", problem.Field)
			}
		})
	}
}

// Patching one field must not disturb the other, and the same range applies.
func TestPatchRepeatAfter(t *testing.T) {
	srv, db := testServerWithDB(t)

	rec := post(t, srv, "/api/v1/monitors",
		`{"name":"site","type":"http","target":"https://example.com"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created monitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	path := "/api/v1/monitors/" + itoa(created.ID)

	if rec := patch(t, srv, path, `{"repeat_after_s":3600}`); rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", rec.Code, rec.Body.String())
	}
	stored, err := db.GetMonitor(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.RepeatAfterS != 3600 {
		t.Errorf("repeat_after_s = %d, want 3600", stored.RepeatAfterS)
	}

	if rec := patch(t, srv, path, `{"repeat_after_s":30}`); rec.Code != http.StatusBadRequest {
		t.Errorf("patch to 30 status = %d, want 400", rec.Code)
	}

	// Patching something unrelated leaves it alone.
	if rec := patch(t, srv, path, `{"name":"renamed"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch name status = %d: %s", rec.Code, rec.Body.String())
	}
	stored, err = db.GetMonitor(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.RepeatAfterS != 3600 {
		t.Errorf("repeat_after_s = %d after an unrelated patch, want 3600", stored.RepeatAfterS)
	}
}
