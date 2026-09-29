package statuspage

import "testing"

// Design §1.3, row by row. The dashboard word each case corresponds to is in
// the name, so a failure says which row of the table broke.
func TestPublicStatusFollowsTheDesignTable(t *testing.T) {
	cases := []struct {
		name string
		live Live
		want Status
	}{
		{"up", Live{Enabled: true, Checked: true}, StatusUp},
		{"dashboard pending (API warning): a failed check, not confirmed", Live{Enabled: true, Checked: true}, StatusUp},
		{"down: confirmed incident", Live{Enabled: true, Checked: true, Confirmed: true}, StatusDown},
		{"recovering: passing, incident still open", Live{Enabled: true, Checked: true, Confirmed: true, Recovering: true}, StatusDegraded},
		{"waiting (API pending): never checked", Live{Enabled: true}, StatusNoData},
		{"paused", Live{Enabled: false, Checked: true}, StatusNotMonitored},
		{"paused during an outage", Live{Enabled: false, Checked: true, Confirmed: true}, StatusNotMonitored},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PublicStatus(c.live); got != c.want {
				t.Errorf("PublicStatus(%+v) = %q, want %q", c.live, got, c.want)
			}
		})
	}
}

// Recovering without a confirmed incident cannot happen in the engine, but
// if a caller ever passes it, the page must not invent an outage.
func TestRecoveringWithoutConfirmedIncidentIsUp(t *testing.T) {
	if got := PublicStatus(Live{Enabled: true, Checked: true, Recovering: true}); got != StatusUp {
		t.Errorf("got %q, want up", got)
	}
}
