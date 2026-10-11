package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Refusals of a new account name the field they are about, in a sentence of
// their own, and a failed write is not passed off as the caller's mistake.

const strongPassword = "correct-horse-battery-staple"

func decodeErrorResponse(t *testing.T, rec *httptest.ResponseRecorder) errorResponse {
	t.Helper()
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body
}

// The sign-in form, the setup form and reset-password all trim the address
// they are given, so an account stored with a space around its address could
// never be signed in to.
func TestCreateUserStoresTheAddressTrimmed(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users",
		`{"email":"  oncall@example.com\t","password":"`+strongPassword+`","role":"editor"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var created userResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Email != "oncall@example.com" {
		t.Errorf("response email = %q, want it trimmed", created.Email)
	}
	if _, err := db.GetUserByEmail(context.Background(), "oncall@example.com"); err != nil {
		t.Errorf("the account cannot be found by the address its owner signs in with: %v", err)
	}
}

func TestSetupStoresTheAddressTrimmed(t *testing.T) {
	db := openEmptyDB(t)
	srv := New(testLogger(), db)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup",
		`{"email":" owner@example.com ","password":"`+strongPassword+`"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if _, err := db.GetUserByEmail(context.Background(), "owner@example.com"); err != nil {
		t.Errorf("the first administrator cannot be found by the trimmed address: %v", err)
	}
}

func TestCreateUserRefusalsNameTheirField(t *testing.T) {
	cases := []struct {
		name, body, field string
	}{
		{"blank email", `{"email":"","password":"` + strongPassword + `"}`, "email"},
		{"email of spaces", `{"email":"   ","password":"` + strongPassword + `"}`, "email"},
		{"email without @", `{"email":"not-an-email","password":"` + strongPassword + `"}`, "email"},
		{"weak password", `{"email":"a@example.com","password":"short"}`, "password"},
		{"unknown role", `{"email":"a@example.com","password":"` + strongPassword + `","role":"god"}`, "role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			rec := httptest.NewRecorder()
			authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users", tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			body := decodeErrorResponse(t, rec)
			if body.Field != tc.field {
				t.Errorf("field = %q, want %q (%s)", body.Field, tc.field, body.Error)
			}
			if strings.HasPrefix(body.Error, "auth:") || strings.Contains(body.Error, "could not create") {
				t.Errorf("error = %q, want the rule in its own words", body.Error)
			}
		})
	}
}

func TestSetupRefusalsNameTheirField(t *testing.T) {
	for body, field := range map[string]string{
		`{"email":"  ","password":"` + strongPassword + `"}`: "email",
		`{"email":"owner@example.com","password":"short"}`:   "password",
	} {
		srv := New(testLogger(), openEmptyDB(t))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/setup", body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400: %s", body, rec.Code, rec.Body)
		}
		if got := decodeErrorResponse(t, rec).Field; got != field {
			t.Errorf("%s: field = %q, want %q", body, got, field)
		}
	}
}

// The column compares without case and, now, without surrounding spaces, so
// each of these is the address already in use.
func TestCreateUserRefusesAnAddressInUseInItsOwnWords(t *testing.T) {
	for _, again := range []string{"dup@example.com", "DUP@Example.com", " dup@example.com "} {
		t.Run(again, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			if _, err := db.CreateUser(context.Background(), "dup@example.com", strongPassword, store.RoleViewer); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users",
				`{"email":"`+again+`","password":"`+strongPassword+`"}`))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			body := decodeErrorResponse(t, rec)
			if body.Field != "email" {
				t.Errorf("field = %q, want email", body.Field)
			}
			if body.Error != "an account with this email address already exists" {
				t.Errorf("error = %q, want the refusal in its own words", body.Error)
			}
		})
	}
}

// A write that fails is the server's fault: a 500 with nothing of the
// database's text, not a 400 telling the administrator to fix their input.
func TestCreateUserReportsAFailedWriteAsAServerError(t *testing.T) {
	srv, db := testServerWithDB(t)
	if _, err := db.Writer.Exec(`CREATE TRIGGER refuse_users BEFORE INSERT ON users
		BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/users",
		`{"email":"new@example.com","password":"`+strongPassword+`"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
	}
	if body := decodeErrorResponse(t, rec); strings.Contains(body.Error, "disk") || body.Field != "" {
		t.Errorf("error = %+v, want a plain server error", body)
	}
}
