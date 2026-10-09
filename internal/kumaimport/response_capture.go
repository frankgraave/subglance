package kumaimport

import (
	"strconv"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
)

// kumaResponseLength is the Response Max Length Kuma 2 gives a monitor until
// someone edits it (RESPONSE_BODY_LENGTH_DEFAULT in src/util.ts in 2.5).
const kumaResponseLength = 1024

// convertResponseCapture carries Kuma 2's Save HTTP Error Response over as
// capture_response, so a monitor whose responses Kuma was told not to keep is
// not given capture by SubGlance's default.
//
// Kuma 2 keeps the body of a failed HTTP check when save_error_response is
// on, cut to response_max_length characters, and the body of a passing one
// only when save_response is on as well (server/model/monitor.js in 2.5). It
// offers the switches on its HTTP types alone, and the migration that added
// them turned error responses on for every monitor
// (db/knex_migrations/2025-10-15-0001 in 2.5). SubGlance keeps the first
// checker.MaxSnapshotBytes of a failed check's body, and never a passing
// check's.
//
// The value is written both ways: off is the case that matters, because the
// usual reason to switch it off is a response that carries a session token
// or personal data, and on keeps the file saying what the monitor does
// rather than leaning on a default. Kuma 1.23 has neither column and kept no
// response; its monitors come over without the field, as before.
//
// A limit is listed only when someone chose it: Kuma's default of 1024 is
// within SubGlance's, and a raised one up to SubGlance's is kept in full. A
// limit above SubGlance's, or below Kuma's default, is listed, as is 0, which
// Kuma's form describes as no limit (2.5 in fact cuts the body to nothing at
// 0, so the note names the setting rather than what Kuma stored).
func convertResponseCapture(m row, out *configfile.Monitor, typ string, note func(string)) {
	switch typ {
	case "http", "keyword", "json-query":
	default:
		return
	}
	if v, ok := m["save_error_response"]; !ok || v == nil {
		return
	}
	on := m.bool("save_error_response")
	out.CaptureResponse = ptr(on)
	if !on {
		return
	}
	if m.bool("save_response") {
		note("Kuma also kept the response of a passing check; SubGlance keeps the response of a failed check only")
	}
	if v, ok := m["response_max_length"]; !ok || v == nil {
		return
	}
	keeps := "SubGlance keeps the first " + strconv.Itoa(checker.MaxSnapshotBytes) + " bytes of a failed check's response"
	switch n := m.int("response_max_length"); {
	case n == 0:
		note("Kuma's Response Max Length was 0, which Kuma describes as no limit; " + keeps)
	case n > checker.MaxSnapshotBytes || n < kumaResponseLength:
		note("Kuma's Response Max Length was " + strconv.Itoa(n) + " characters; " + keeps)
	}
}
