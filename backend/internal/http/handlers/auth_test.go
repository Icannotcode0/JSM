package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	jsmHttp "github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
)

/* ---------- fakes --------------------------------------------------------- */

type fakeAuthService struct {
	cookies    []*http.Cookie
	err        error
	gotUserID  string
	gotSession string
	gotReq     domain.ChangePasswordRequest
	gotSignUp  domain.SignUpRequest
	user       domain.User
	calls      int
}

func (f *fakeAuthService) Authenticate(_ context.Context, _, _ string) ([]*http.Cookie, error) {
	return f.cookies, f.err
}

// Present so the fake satisfies service.Authenticator. SignUp has its own
// tests; these cases are about the change-password path.
func (f *fakeAuthService) CreateUser(_ context.Context, req domain.SignUpRequest) (domain.User, error) {
	f.calls++
	f.gotSignUp = req
	return f.user, f.err
}

func (f *fakeAuthService) Logout(_ context.Context, _ string) ([]*http.Cookie, error) {
	return f.cookies, f.err
}
func (f *fakeAuthService) ChangePassword(_ context.Context, userID, sessionID string, req domain.ChangePasswordRequest) ([]*http.Cookie, error) {
	f.calls++
	f.gotUserID, f.gotSession, f.gotReq = userID, sessionID, req
	return f.cookies, f.err
}

// fakeSessions stands in for *SessionManager's one method the handler needs.
type fakeSessions struct{ sid string }

func (f fakeSessions) SessionIDFromRequest(_ *http.Request) string { return f.sid }

func newResetRequest(body string, userID string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/reset-password", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if userID != "" {
		// Mirrors what SessionRequired puts on the context.
		r = r.WithContext(authentication.ContextWithUserID(r.Context(), userID))
	}
	return r
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the JSON error envelope: %q", rec.Body.String())
	}
	return body.Error
}

/* ---------- tests --------------------------------------------------------- */

// Without a user on the context the route was mounted wrong. It must fail
// closed, and it must write exactly one response — an earlier version wrote a
// 401 from requireUser and then a 400 on top of it.
func TestResetPasswordUnauthenticatedWritesSingle401(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{})

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, newResetRequest(`{"current_password":"a","new_password":"B1!aaaaa"}`, ""))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if svc.calls != 0 {
		t.Error("service was called for an unauthenticated request")
	}
	// Two WriteJSONError calls would concatenate two JSON objects.
	if n := strings.Count(rec.Body.String(), `"error"`); n != 1 {
		t.Errorf("response contains %d error objects, want 1: %q", n, rec.Body.String())
	}
}

func TestResetPasswordMalformedBody(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{sid: "sid"})

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, newResetRequest(`{nope`, "uid-1"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if svc.calls != 0 {
		t.Error("service was called with an undecodable body")
	}
}

func TestResetPasswordOversizedBody(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{sid: "sid"})

	huge := `{"current_password":"a","new_password":"` + strings.Repeat("x", jsmHttp.MaxBodyBytes+100) + `"}`
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, newResetRequest(huge, "uid-1"))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

// The session being ended is the one presenting the request. The handler is the
// only layer that can read it, so this asserts it is actually passed down —
// an earlier version passed the user ID, which silently deleted nothing.
func TestResetPasswordForwardsSessionAndUser(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{sid: "session-xyz"})

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, newResetRequest(
		`{"current_password":"Old1!aaa","new_password":"New1!aaa"}`, "user-abc"))

	if svc.calls != 1 {
		t.Fatalf("service called %d times, want 1", svc.calls)
	}
	if svc.gotUserID != "user-abc" {
		t.Errorf("userID = %q, want user-abc", svc.gotUserID)
	}
	if svc.gotSession != "session-xyz" {
		t.Errorf("sessionID = %q, want session-xyz — the wrong value silently deletes nothing", svc.gotSession)
	}
	if svc.gotReq.CurrentPassword != "Old1!aaa" || svc.gotReq.NewPassword != "New1!aaa" {
		t.Errorf("request body not forwarded: %+v", svc.gotReq)
	}
}

func TestResetPasswordSuccessSetsCookiesAndBody(t *testing.T) {
	svc := &fakeAuthService{cookies: []*http.Cookie{
		{Name: "jsm_session", Value: "", MaxAge: -1},
		{Name: "jsm_csrf", Value: "nonce!.sig"},
	}}
	h := NewAuth(svc, fakeSessions{sid: "sid"})

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, newResetRequest(
		`{"current_password":"Old1!aaa","new_password":"New1!aaa"}`, "uid"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := len(rec.Result().Cookies()); got != 2 {
		t.Errorf("set %d cookies, want 2", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("no JSON body: %q", rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf("body = %v, want {status: ok}", body)
	}
}

// A wrong *current* password is 401, and a policy failure is 400 carrying the
// service's own wording — a user told "INTERNAL_SERVER_ERROR" for a short
// password cannot fix it.
func TestResetPasswordErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"wrong current password", metrics.ErrIncorrectCredentials, http.StatusUnauthorized, "INCORRECT_CREDENTIALS"},
		{"policy failure", metrics.ErrInsufficientPasswordLength, http.StatusBadRequest, "password must be at least 8 characters"},
		{"identical", metrics.ErrIdenticalPasswords, http.StatusBadRequest, "new password must differ from the current one"},
		{"session user gone", metrics.ErrUserNotFound, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "INTERNAL_SERVER_ERROR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAuth(&fakeAuthService{err: tc.err}, fakeSessions{sid: "sid"})
			rec := httptest.NewRecorder()
			h.ResetPassword(rec, newResetRequest(
				`{"current_password":"Old1!aaa","new_password":"New1!aaa"}`, "uid"))

			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if got := decodeError(t, rec); got != tc.wantBody {
				t.Errorf("error = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

// An internal failure must not describe the schema or the driver to a caller.
func TestResetPasswordDoesNotLeakInternalErrors(t *testing.T) {
	h := NewAuth(&fakeAuthService{
		err: errors.New("mongo: collection jobtracker.users index _id_ failed"),
	}, fakeSessions{sid: "sid"})

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, newResetRequest(
		`{"current_password":"Old1!aaa","new_password":"New1!aaa"}`, "uid"))

	if strings.Contains(rec.Body.String(), "jobtracker") {
		t.Errorf("internal detail leaked to the client: %q", rec.Body.String())
	}
}
