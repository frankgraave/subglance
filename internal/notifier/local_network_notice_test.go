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

// The resolver's notice points at the resolver, says how long, and says the
// host could still reach the internet, so nobody goes looking at the uplink.
func TestLocalDNSNoticeNamesTheResolver(t *testing.T) {
	from := time.Date(2026, 10, 11, 3, 12, 0, 0, time.UTC)
	a := LocalDNSNotice(from, from.Add(12*time.Minute))
	if a.Title() != "SubGlance's DNS resolver is answering again" {
		t.Fatalf("title = %q", a.Title())
	}
	if a.Down() {
		t.Fatal("a notice that the resolver is back reads as bad news")
	}
	body := a.Body()
	for _, want := range []string{"DNS resolver", "12 minutes", "2026-10-11 03:12 UTC", "03:24 UTC", "could still reach the internet", "not counted as outages"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q does not say %q", body, want)
		}
	}
	if sms := smsText(a, "", 160); !strings.Contains(sms, "ONLINE SubGlance DNS again after 12 min") {
		t.Errorf("SMS = %q", sms)
	}
}
