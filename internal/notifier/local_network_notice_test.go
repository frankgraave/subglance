package notifier

import (
	"strings"
	"testing"
	"time"
)

func TestLocalNetworkNoticeSaysHowLongAndWhatWasNotCounted(t *testing.T) {
	from := time.Date(2026, 9, 28, 3, 12, 0, 0, time.UTC)
	a := LocalNetworkNotice(from, from.Add(47*time.Minute))

	if a.Title() != "SubGlance is back online" {
		t.Fatalf("title = %q", a.Title())
	}
	if a.Down() {
		t.Fatal("a notice that the connection is back reads as bad news")
	}
	body := a.Body()
	for _, want := range []string{"47 minutes", "2026-09-28 03:12 UTC", "03:59 UTC", "not counted as outages"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q does not say %q", body, want)
		}
	}
}
