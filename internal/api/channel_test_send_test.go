package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// stubTester stands in for the notifier.
type stubTester struct {
	err    error
	called int
	got    store.Channel
}

func (s *stubTester) Test(_ context.Context, ch store.Channel) error {
	s.called++
	s.got = ch
	return s.err
}

func TestChannelTestSendsThroughTheChannel(t *testing.T) {
	srv, db := testServerWithDB(t)
	tester := &stubTester{}
	srv = srv.WithChannelTester(tester)

	ch, err := db.CreateChannel(context.Background(), store.Channel{
		Name:    "ops",
		Type:    store.ChannelWebhook,
		Config:  map[string]string{"url": "https://example.test/hook"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels/"+itoa(ch.ID)+"/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if tester.called != 1 {
		t.Fatalf("tester called %d times, want 1", tester.called)
	}
	// The handler must pass the stored config, not the masked one the API
	// returns on read: a test that sends with a masked token proves
	// nothing and would fail confusingly.
	if tester.got.Config["url"] != "https://example.test/hook" {
		t.Errorf("tester got config %v, want the stored url", tester.got.Config)
	}
}

// TestChannelTestReportsTheRealError is the point of the button: an operator
// has to learn that the token is wrong, not that something failed.
func TestChannelTestReportsTheRealError(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv = srv.WithChannelTester(&stubTester{err: errors.New("endpoint rejected the alert (401)")})

	ch, err := db.CreateChannel(context.Background(), store.Channel{
		Name:    "ops",
		Type:    store.ChannelSlack,
		Config:  map[string]string{"url": "https://hooks.slack.test/x"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels/"+itoa(ch.ID)+"/test", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}

	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OK {
		t.Error("ok = true on a failed test")
	}
	if body.Error != "endpoint rejected the alert (401)" {
		t.Errorf("error = %q, want the real cause", body.Error)
	}
}

func TestChannelTestUnknownChannelIs404(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv = srv.WithChannelTester(&stubTester{})

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels/999/test", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
