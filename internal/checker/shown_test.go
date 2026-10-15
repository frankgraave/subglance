package checker

import "testing"

func TestShownURLKeepsSchemeAndHostOnly(t *testing.T) {
	for raw, want := range map[string]string{
		"https://hc-ping.com/secret-uuid-1234":                 "https://hc-ping.com/…",
		"https://hc-ping.com":                                  "https://hc-ping.com",
		"https://hc-ping.com/":                                 "https://hc-ping.com/",
		"https://hc-ping.com?key=k3y-value":                    "https://hc-ping.com/…",
		"https://hc-ping.com/#fr4g":                            "https://hc-ping.com/…",
		"http://user:pa55word@[2001:db8::1]:8080/ping?k=v":     "http://[2001:db8::1]:8080/…",
		"http://subglance-b:3000/api/v1/push/secret-uuid-1234": "http://subglance-b:3000/…",
		"https://user:pa55word/x@hc-ping.com/secret-uuid-1234": "https://***@hc-ping.com/…",
		"hc-ping.com/secret-uuid-1234":                         "hc-ping.com/…",
		"https://user:pa55word/secret-uuid-1234":               "https://user:***/…",
		"https://hc-ping.com:abc/secret-uuid-1234":             "https://hc-ping.com:***/…",
		"https://hc-ping.com:abc/ping?key@secret-uuid-1234":    "https://hc-ping.com:***/…",
		"https://hc-ping.com:abc/ping#fr4g@secret-uuid-1234":   "https://hc-ping.com:***/…",
		"http://[2001:db8::1]:pa55word/x":                      "http://[2001:db8::1]:***/…",
	} {
		if got := ShownURL(raw); got != want {
			t.Errorf("ShownURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
