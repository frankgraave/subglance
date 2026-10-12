package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Refusals of a password change name the field they are about, so the
// Settings form can put each one under its input and tell a wrong current
// password from an expired session without matching the sentence.

// changePasswordAs signs in as a fresh admin and posts body to the password
// route with that session.
func changePasswordAs(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv, db := testServerWithDB(t)
	h := srv.Handler()
	if _, err := db.CreateUser(t.Context(), "user@example.com", strongPassword, store.RoleAdmin); err != nil {
		t.Fatalf("create user: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/login",
		`{"email":"user@example.com","password":"`+strongPassword+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	req := jsonRequest(http.MethodPost, "/api/v1/auth/password", body)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPasswordChangeNamesTheNewPasswordWhenItIsWeak(t *testing.T) {
	for name, tc := range map[string]struct{ password, want string }{
		"too short": {"short", "at least 12 characters"},
		"too long":  {strings.Repeat("a", 1025), "at most 1024 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := changePasswordAs(t,
				`{"current_password":"`+strongPassword+`","new_password":"`+tc.password+`"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			got := decodeErrorResponse(t, rec)
			if got.Field != "new_password" {
				t.Errorf("field = %q, want new_password", got.Field)
			}
			if !strings.Contains(got.Error, tc.want) {
				t.Errorf("error = %q, want it to say %q", got.Error, tc.want)
			}
			// The package prefix is how Go errors are wrapped, not words a
			// person changing a password should read.
			if strings.HasPrefix(got.Error, "auth:") {
				t.Errorf("error = %q carries the package prefix", got.Error)
			}
		})
	}
}

func TestPasswordChangeNamesTheCurrentPasswordWhenItIsWrong(t *testing.T) {
	rec := changePasswordAs(t,
		`{"current_password":"not-the-right-one","new_password":"a-completely-new-passphrase"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	got := decodeErrorResponse(t, rec)
	if got.Field != "current_password" {
		t.Errorf("field = %q, want current_password", got.Field)
	}
	if got.Error != "current password is incorrect" {
		t.Errorf("error = %q, want the sentence clients already show", got.Error)
	}
}

// An expired or missing session is the same status with no field: that
// absence is what tells the two 401s apart.
func TestPasswordChangeWithoutASessionNamesNoField(t *testing.T) {
	srv, _ := testServerWithDB(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"x","new_password":"a-completely-new-passphrase"}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if got := decodeErrorResponse(t, rec); got.Field != "" {
		t.Errorf("field = %q, want none on a missing session", got.Field)
	}
}
