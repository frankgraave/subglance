package api

import "net/http"

// problem is a rejected request, carrying the sentence the user reads and,
// when the validator knows it, the request field the complaint is about.
//
// The field exists because a message alone cannot be placed. A form that only
// receives "an http monitor needs a target starting with http:// or https://"
// has two options: render it in a global region far from the input that caused
// it, or guess the input by matching on the server\'s wording. The second is
// worse than it looks — it keeps working until someone rewords a message, and
// then it fails silently, because the text still renders, just in the wrong
// place. Nothing catches that.
//
// So the validator that already knows which field it rejected says so, and the
// client places the message without knowing what it says. The zero value means
// "no problem"; a problem always has a message, and may have a field.
type problem struct {
	// field is the JSON name from the request body, for example "target" or
	// "ssl_warn_days" — the name the client sent, not the Go field, because
	// the client is what has to find the input again.
	field string
	msg   string
}

// ok reports whether the value represents a passing validation.
func (p problem) ok() bool { return p.msg == "" }

// fieldProblem rejects a named request field.
func fieldProblem(field, msg string) problem {
	return problem{field: field, msg: msg}
}

// bodyProblem rejects the request without blaming a single field. Use it when
// the complaint genuinely spans fields, or is about the request rather than
// its contents — a malformed body, a rate limit, a missing session. Tagging
// those with a field would point a form at an input that is not wrong.
func bodyProblem(msg string) problem {
	return problem{msg: msg}
}

// errorResponse is the body of every error this API returns.
//
// Field is omitted rather than sent empty so that a client can test for its
// presence: "" and "absent" would otherwise be the same value in JavaScript,
// and a global error would look like a field error for the field named "".
type errorResponse struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

// writeProblem renders a problem, field and all.
func writeProblem(w http.ResponseWriter, status int, p problem) {
	writeJSON(w, status, errorResponse{Error: p.msg, Field: p.field})
}
