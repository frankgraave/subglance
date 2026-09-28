package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/connectivity"
)

// On by default, as decided for the feature: the check only sends traffic at
// the moment an alert would go out, and what it prevents is a false alert for
// every monitor at once.
func TestConnectivityCheckDefaultsToOnWithTheDocumentedTargets(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ConnectivityCheck {
		t.Fatal("ConnectivityCheck defaults to off")
	}
	if got := c.ConnectivityTargetList(); !reflect.DeepEqual(got, connectivity.DefaultTargets) {
		t.Fatalf("targets = %v, want %v", got, connectivity.DefaultTargets)
	}
}

func TestConnectivityCheckSources(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		args        []string
		wantOn      bool
		wantTargets []string
	}{
		{name: "flag off", args: []string{"--connectivity-check=false"}, wantOn: false},
		{name: "environment off", env: map[string]string{"SUBGLANCE_CONNECTIVITY_CHECK": "false"}, wantOn: false},
		{name: "flag over environment", env: map[string]string{"SUBGLANCE_CONNECTIVITY_CHECK": "false"},
			args: []string{"--connectivity-check=true"}, wantOn: true, wantTargets: connectivity.DefaultTargets},
		{name: "own targets from the environment",
			env:    map[string]string{"SUBGLANCE_CONNECTIVITY_TARGETS": "router-a:443, router-b:80"},
			wantOn: true, wantTargets: []string{"router-a:443", "router-b:80"}},
		{name: "own targets from a flag", args: []string{"--connectivity-targets=gateway:53"},
			wantOn: true, wantTargets: []string{"gateway:53"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, err := Load(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if c.ConnectivityCheck != tc.wantOn {
				t.Fatalf("ConnectivityCheck = %v, want %v", c.ConnectivityCheck, tc.wantOn)
			}
			if got := c.ConnectivityTargetList(); !reflect.DeepEqual(got, tc.wantTargets) {
				t.Fatalf("targets = %v, want %v", got, tc.wantTargets)
			}
		})
	}
}

// A mistyped target is a startup error. A canary that can never connect would
// call the host offline during every real outage and hide all of them.
func TestConnectivityTargetsAreValidated(t *testing.T) {
	for _, bad := range []string{"gateway", "gateway:dns", ",", "gateway:0"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("SUBGLANCE_CONNECTIVITY_TARGETS", bad)
			_, err := Load(nil)
			if err == nil || !strings.Contains(err.Error(), "connectivity-targets") {
				t.Fatalf("Load accepted %q: %v", bad, err)
			}
		})
	}
	// Off means the targets are not used, so they are not judged either.
	t.Setenv("SUBGLANCE_CONNECTIVITY_TARGETS", "gateway")
	if _, err := Load([]string{"--connectivity-check=false"}); err != nil {
		t.Fatalf("targets were validated with the check off: %v", err)
	}
}
