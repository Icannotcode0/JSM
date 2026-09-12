package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jsmHttp "github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func newSignUpRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

const validSignUpBody = `{"email":"ada@example.com","password":"ValidPass1!","name":"Ada"}`

func TestSignUpSucceeds(t *testing.T) {
	svc := &fakeAuthService{user: domain.User{
		ID:           bson.NewObjectID(),
		Email:        "ada@example.com",
		Name:         "Ada",
		PasswordHash: "$2a$10$averysecretbcrypthashvalue",
	}}
	h := NewAuth(svc, fakeSessions{}, noLimit())

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(validSignUpBody))

	// 201 with the created user, per API.md.
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("no JSON body: %q", rec.Body.String())
	}
	if body.User == nil {
		t.Fatalf(`response has no "user" key: %s`, rec.Body.String())
	}
	if body.User["email"] != "ada@example.com" {
		t.Errorf("email = %v", body.User["email"])
	}
}

// domain.User tags PasswordHash `json:"-"`, and this is what keeps that true:
// the signup response is the first place a freshly created hash could escape.
func TestSignUpNeverReturnsThePasswordHash(t *testing.T) {
	svc := &fakeAuthService{user: domain.User{
		Email:        "ada@example.com",
		PasswordHash: "$2a$10$averysecretbcrypthashvalue",
	}}
	h := NewAuth(svc, fakeSessions{}, noLimit())

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(validSignUpBody))

	for _, leak := range []string{"password_hash", "PasswordHash", "$2a$10$averysecret"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("%q leaked in the response: %s", leak, rec.Body.String())
		}
	}
}

// Signup is the one authenticated-user-creating endpoint that must work with no
// session at all — requiring one would make the first account impossible.
func TestSignUpNeedsNoSession(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{}, noLimit())

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(validSignUpBody)) // no user on the context

	if rec.Code == http.StatusUnauthorized {
		t.Error("signup demanded a session")
	}
	if svc.calls == 0 {
		t.Error("the service was never reached")
	}
}

func TestSignUpMalformedBody(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{}, noLimit())

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(`{nope`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if svc.calls != 0 {
		t.Error("the service was called with an undecodable body")
	}
}

func TestSignUpOversizedBody(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{}, noLimit())

	huge := `{"email":"a@b.co","password":"ValidPass1!","name":"` +
		strings.Repeat("x", jsmHttp.MaxBodyBytes+100) + `"}`

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(huge))

	// 413 rather than 400: retrying the same payload is pointless, and the
	// client needs to know that rather than assume it sent bad JSON.
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if svc.calls != 0 {
		t.Error("an oversized body still reached the service")
	}
}

func TestSignUpForwardsTheRequestBody(t *testing.T) {
	svc := &fakeAuthService{}
	h := NewAuth(svc, fakeSessions{}, noLimit())

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(validSignUpBody))

	if svc.calls != 1 {
		t.Fatalf("service called %d times, want 1", svc.calls)
	}
	if svc.gotSignUp.Email != "ada@example.com" ||
		svc.gotSignUp.Password != "ValidPass1!" ||
		svc.gotSignUp.Name != "Ada" {
		t.Errorf("body not forwarded intact: %+v", svc.gotSignUp)
	}
}

func TestSignUpErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{
			// 409, not 401 or 400: the request was authentic and well-formed,
			// the address is simply already in use.
			"email taken", metrics.ErrEmailTaken,
			http.StatusConflict, metrics.CodeEmailAlreadyTaken,
		},
		{
			"invalid email", metrics.ErrInvalidEmail,
			http.StatusBadRequest, "a valid email address is required",
		},
		{
			"weak password", metrics.ErrInsufficientPasswordLength,
			http.StatusBadRequest, "password must be at least 8 characters",
		},
		{
			"missing uppercase", metrics.ErrMissingUppercase,
			http.StatusBadRequest, "password must contain an uppercase letter",
		},
		{
			"unknown failure", errors.New("boom"),
			http.StatusInternalServerError, metrics.CodeInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAuth(&fakeAuthService{err: tc.err}, fakeSessions{}, noLimit())
			rec := httptest.NewRecorder()
			h.SignUp(rec, newSignUpRequest(validSignUpBody))

			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if got := decodeError(t, rec); got != tc.wantBody {
				t.Errorf("error = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

// Validation failures are returned verbatim because every Reason is a literal
// written in the service. Anything unrecognised must not be, or a driver
// message describing the schema reaches an unauthenticated caller.
func TestSignUpDoesNotLeakInternalErrors(t *testing.T) {
	h := NewAuth(&fakeAuthService{
		err: errors.New("mongo: jobtracker.users index email_1 dup key"),
	}, fakeSessions{}, noLimit())

	rec := httptest.NewRecorder()
	h.SignUp(rec, newSignUpRequest(validSignUpBody))

	for _, leak := range []string{"jobtracker", "index", "dup key"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("internal detail %q leaked: %s", leak, rec.Body.String())
		}
	}
}

// A failed signup must not answer 201 — a client that trusted the status would
// send the user to a login page for an account that was never created.
func TestSignUpDoesNotReport201OnFailure(t *testing.T) {
	for _, err := range []error{
		metrics.ErrEmailTaken,
		metrics.ErrInvalidEmail,
		errors.New("boom"),
	} {
		h := NewAuth(&fakeAuthService{err: err}, fakeSessions{}, noLimit())
		rec := httptest.NewRecorder()
		h.SignUp(rec, newSignUpRequest(validSignUpBody))

		if rec.Code == http.StatusCreated {
			t.Errorf("%v produced a 201", err)
		}
		if n := strings.Count(rec.Body.String(), `"error"`); n != 1 {
			t.Errorf("%v produced %d error objects, want 1: %s", err, n, rec.Body.String())
		}
	}
}
