package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"time"

	"github.com/frankgraave/subglance/internal/mutelink"
	"github.com/frankgraave/subglance/internal/store"
)

// The page behind the link in an alert that mutes its incident's repeat
// alerts (internal/mutelink). It is public: the signed token in the path is
// the credential, and it authorises one thing on one incident.
//
// Following the link never mutes anything. Slack, Teams and most mail
// scanners open every link in a message to draw a preview or check it, and
// a GET with a side effect would mute the incident before anyone read the
// alert. The page names the monitor and the incident and carries one
// button, a POST back to the same address; only that mutes.

// mutePathPattern is the route; mutelink.PathPrefix is the same path as the
// links are written.
const mutePathPattern = "/mute/{token}"

// WithMuteLinks sets the signer that checks the links. Without one every
// link is answered as one that does not work.
func (s *Server) WithMuteLinks(signer *mutelink.Signer) *Server {
	s.muteLinks = signer
	return s
}

// muteView is what the page shows. Every field is escaped by html/template.
type muteView struct {
	Heading string
	Text    string
	// Monitor and Since describe the incident; empty on a link that does
	// not work, which names nothing.
	Monitor string
	Since   string
	// Button is set only while the incident can still be muted.
	Button string
	Note   string
}

// muteStyle is the page's only stylesheet, allowed by hash in its policy.
// System fonts and two colour schemes: the page is read once, on a phone,
// often at night.
const muteStyle = `:root{color-scheme:light dark;--ink:#1b1f24;--muted:#5b636e;--paper:#f6f7f9;--card:#fff;--line:#d9dde3;--accent:#1f6feb;--on-accent:#fff}
@media (prefers-color-scheme:dark){:root{--ink:#e6e8eb;--muted:#a3abb6;--paper:#0f1216;--card:#171b21;--line:#2a3038;--accent:#4c8dff;--on-accent:#0f1216}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:var(--paper);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{width:100%;max-width:440px;background:var(--card);border:1px solid var(--line);border-radius:12px;padding:24px}
h1{font-size:20px;line-height:1.3;margin:0 0 8px}p{margin:0 0 12px}.muted{color:var(--muted);font-size:14px}
dl{margin:0 0 16px;display:grid;grid-template-columns:auto 1fr;gap:4px 12px}dt{color:var(--muted)}dd{margin:0;overflow-wrap:anywhere}
button{font:inherit;font-weight:600;min-height:44px;width:100%;padding:10px 16px;border:0;border-radius:8px;background:var(--accent);color:var(--on-accent);cursor:pointer}
button:focus-visible{outline:3px solid var(--ink);outline-offset:2px}`

var muteTemplate = template.Must(template.New("mute").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Heading}} · SubGlance</title>
<style>` + muteStyle + `</style>
</head><body><main>
<h1>{{.Heading}}</h1>
{{if .Monitor}}<dl><dt>Monitor</dt><dd>{{.Monitor}}</dd>{{if .Since}}<dt>Down since</dt><dd>{{.Since}}</dd>{{end}}</dl>{{end}}
{{if .Text}}<p>{{.Text}}</p>{{end}}
{{if .Button}}<form method="post"><button type="submit">{{.Button}}</button></form>{{end}}
{{if .Note}}<p class="muted">{{.Note}}</p>{{end}}
</main></body></html>
`))

// mutePolicy is the page's Content-Security-Policy: its stylesheet by hash,
// a form that may post only back to this origin, and nothing else.
var mutePolicy = func() string {
	sum := sha256.Sum256([]byte(muteStyle))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; " +
		"form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
}()

// muteTarget is the incident a link points at, read for the page.
type muteTarget struct {
	incident store.Incident
	monitor  string
}

// handleMutePage answers a followed link with what it would do. It reads
// and never writes, whoever or whatever follows it.
func (s *Server) handleMutePage(w http.ResponseWriter, r *http.Request) {
	s.serveMute(w, r, false)
}

// handleMute is the page's button: it mutes the incident's repeat alerts.
func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	s.serveMute(w, r, true)
}

func (s *Server) serveMute(w http.ResponseWriter, r *http.Request, act bool) {
	now := time.Now()
	if s.muteLinks == nil {
		writeMutePage(w, http.StatusNotFound, muteNotWorking)
		return
	}
	claim, err := s.muteLinks.Verify(r.PathValue("token"), now)
	if errors.Is(err, mutelink.ErrExpired) {
		writeMutePage(w, http.StatusGone, muteView{
			Heading: "This link has expired",
			Text:    "A link to mute an alert works for 24 hours. Sign in to SubGlance to mute the incident from there.",
		})
		return
	}
	if err != nil {
		writeMutePage(w, http.StatusNotFound, muteNotWorking)
		return
	}

	target, err := s.muteTarget(r.Context(), claim)
	if errors.Is(err, store.ErrNotFound) {
		writeMutePage(w, http.StatusNotFound, muteNotWorking)
		return
	}
	if err != nil {
		s.log.Error("read incident for mute link", "incident_id", claim.IncidentID, "error", err)
		writeMutePage(w, http.StatusServiceUnavailable, muteView{
			Heading: "SubGlance cannot answer right now",
			Text:    "Nothing was changed. Try the link again in a minute.",
		})
		return
	}

	inc := target.incident
	view := muteView{Monitor: target.monitor, Since: muteTime(inc.StartedAt)}
	switch {
	case inc.Resolved():
		view.Heading = "This incident is over"
		view.Text = "The monitor is back up, so there are no repeat alerts left to mute."
		view.Since = ""
	case inc.Acked():
		view.Heading = "Repeat alerts are muted"
		view.Text = "Repeat alerts for this incident were already muted, on " + muteTime(inc.AckedAt) +
			". You will still get an alert when the monitor is back up."
	case !act:
		view.Heading = "Mute repeat alerts?"
		view.Text = "SubGlance stops sending reminders about this incident. You will still get an alert when the monitor is back up."
		view.Button = "Mute repeat alerts"
		view.Note = "This link works until the incident ends, or until " + muteTime(claim.Expires) + "."
	default:
		if err := s.db.AckIncident(r.Context(), inc.ID, now); err != nil {
			s.log.Error("mute incident from alert link", "incident_id", inc.ID, "error", err)
			writeMutePage(w, http.StatusServiceUnavailable, muteView{
				Heading: "SubGlance cannot answer right now",
				Text:    "The incident was not muted. Try the button again in a minute.",
			})
			return
		}
		s.log.Info("incident acknowledged from an alert link",
			"incident_id", inc.ID, "channel_id", claim.ChannelID)
		view.Heading = "Repeat alerts are muted"
		view.Text = "SubGlance sends no more reminders about this incident. You will still get an alert when the monitor is back up."
	}
	writeMutePage(w, http.StatusOK, view)
}

// muteTarget reads the incident a claim names, its monitor's name, and
// whether the channel the link was sent to still exists. A removed channel
// takes its links with it: removing one is how a link that reached the
// wrong chat is taken back.
func (s *Server) muteTarget(ctx context.Context, claim mutelink.Claim) (muteTarget, error) {
	if _, err := s.db.GetChannel(ctx, claim.ChannelID); err != nil {
		return muteTarget{}, err
	}
	inc, err := s.db.GetIncident(ctx, claim.IncidentID)
	if err != nil {
		return muteTarget{}, err
	}
	name := ""
	if m, err := s.db.GetMonitor(ctx, inc.MonitorID); err == nil {
		name = m.Name
	}
	return muteTarget{incident: inc, monitor: name}, nil
}

// muteNotWorking is the one answer for a link this instance did not sign as
// it is, and for one whose incident or channel is gone. It names nothing,
// so it tells a prober nothing.
var muteNotWorking = muteView{
	Heading: "This link does not work",
	Text: "It may have been cut short or changed when it was copied, or the channel it was sent to was removed. " +
		"Sign in to SubGlance to mute the incident from there.",
}

// muteTime writes a time on the page. The server does not know the
// reader's time zone, so it says UTC rather than guess.
func muteTime(t time.Time) string {
	return t.UTC().Format("2 Jan 2006, 15:04") + " UTC"
}

func writeMutePage(w http.ResponseWriter, status int, view muteView) {
	var buf bytes.Buffer
	if err := muteTemplate.Execute(&buf, view); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", mutePolicy)
	// The token is a credential: no cache keeps the page, and no request
	// from it names the address it came from.
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes()) //nolint:gosec // G705: html/template output
}
