package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestHeaderProblem(t *testing.T) {
	cases := map[string]struct {
		headers map[string]string
		want    string // a fragment of the message; "" means accepted
	}{
		"none":              {nil, ""},
		"empty":             {map[string]string{}, ""},
		"ordinary":          {map[string]string{"Authorization": "Bearer x", "X-Api-Key": "k"}, ""},
		"empty value":       {map[string]string{"X-Empty": ""}, ""},
		"tab in value":      {map[string]string{"X-Tab": "a\tb"}, ""},
		"space in name":     {map[string]string{"X Api": "k"}, `"X Api" is not valid`},
		"colon in name":     {map[string]string{"X-Api:": "k"}, `"X-Api:" is not valid`},
		"empty name":        {map[string]string{"": "k"}, `"" is not valid`},
		"newline in value":  {map[string]string{"X-Api": "a\nb"}, `"X-Api" has a line break`},
		"carriage return":   {map[string]string{"X-Api": "a\rb"}, `"X-Api" has a line break`},
		"nul in value":      {map[string]string{"X-Api": "a\x00b"}, `"X-Api" has a line break`},
		"same name in case": {map[string]string{"X-Api": "a", "x-api": "b"}, `"X-Api" and "x-api" are one header`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := RequestHeaderProblem(tc.headers)
			if tc.want == "" && got != "" {
				t.Fatalf("refused: %s", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("problem = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// TestRequestHeaderProblemMatchesTheTransport pins the rule to what it
// guards against: every header it refuses for its name or value makes the
// check fail without reaching the server, and every one it accepts arrives.
// If net/http ever loosens or tightens its rules, this is the test that says
// so.
func TestRequestHeaderProblemMatchesTheTransport(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	for _, h := range []map[string]string{
		{"X-Api": "k"}, {"X-Tab": "a\tb"}, {"X-Empty": ""}, {"!#$%&'*+-.^_`|~": "v"},
		{"X Api": "k"}, {"X-Api:": "k"}, {"": "k"}, {"X-Api": "a\nb"}, {"X-Api": "a\x00b"}, {"X-Api": "a\x7fb"}, {"Ünïcode": "v"}, {"X-Utf8": "café"},
	} {
		reached = false
		m := monitor(srv.URL)
		m.Headers = h
		res := testChecker().Check(context.Background(), m)
		if accepted := RequestHeaderProblem(h) == ""; accepted != reached || accepted != res.OK {
			t.Errorf("%q: accepted = %v, but the server was reached = %v (check ok = %v, %s)",
				h, accepted, reached, res.OK, res.Error)
		}
	}
}
