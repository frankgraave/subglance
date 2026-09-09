package checker

import "testing"

func TestParseStatusMatcher(t *testing.T) {
	tests := []struct {
		spec    string
		matches []int
		rejects []int
	}{
		{"", []int{200, 204, 299}, []int{199, 300, 404, 500}},
		{"200", []int{200}, []int{201, 199}},
		{"200-299", []int{200, 250, 299}, []int{199, 300}},
		{"200,301,302", []int{200, 301, 302}, []int{300, 303, 404}},
		{"2xx", []int{200, 250, 299}, []int{199, 300}},
		{"2xx,3xx", []int{200, 299, 300, 399}, []int{199, 400}},
		{"200-204,301", []int{200, 204, 301}, []int{205, 300, 302}},
		{"404", []int{404}, []int{200}},
		{" 200 , 301 ", []int{200, 301}, []int{302}},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			m, err := ParseStatusMatcher(tt.spec)
			if err != nil {
				t.Fatalf("ParseStatusMatcher(%q): %v", tt.spec, err)
			}
			for _, code := range tt.matches {
				if !m.Matches(code) {
					t.Errorf("%q should match %d", tt.spec, code)
				}
			}
			for _, code := range tt.rejects {
				if m.Matches(code) {
					t.Errorf("%q should not match %d", tt.spec, code)
				}
			}
		})
	}
}

func TestParseStatusMatcherRejectsGarbage(t *testing.T) {
	bad := []string{
		"abc",
		"99",      // below the valid range
		"600",     // above the valid range
		"299-200", // inverted
		"200-",
		"-200",
		"2yy",
		"7xx",
		",",
	}
	for _, spec := range bad {
		t.Run(spec, func(t *testing.T) {
			if _, err := ParseStatusMatcher(spec); err == nil {
				t.Errorf("ParseStatusMatcher(%q) succeeded, want an error", spec)
			}
		})
	}
}

func TestStatusMatcherKeepsItsSpec(t *testing.T) {
	m, err := ParseStatusMatcher("200-204,301")
	if err != nil {
		t.Fatal(err)
	}
	if m.String() != "200-204,301" {
		t.Errorf("String() = %q, want the original spec", m.String())
	}
}

// An empty spec must default to 2xx, which is what nearly everyone means and
// what the database default records.
func TestEmptySpecDefaultsToSuccess(t *testing.T) {
	m, err := ParseStatusMatcher("")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Matches(200) || m.Matches(500) {
		t.Errorf("empty spec did not behave as 200-299 (spec is %q)", m)
	}
}
