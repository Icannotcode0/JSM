package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	authen "github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/config"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

/* ---------- fake store ---------------------------------------------------
   Hand-written rather than generated: the Authenticator surface is three
   methods, and a fake that records what it was asked lets the tests assert on
   the *arguments* too — which is where the change-password bug lived.
   ------------------------------------------------------------------------- */

type fakeUserStore struct {
	user        domain.User
	lookupErr   error
	editErr     error
	editedID    string
	editedHash  string
	editCalls   int
	registerErr error
	registered  []domain.User
	// lookedUp records the address Authenticate actually queried with, which
	// is the only place to see whether it normalised before looking up.
	lookedUp []string
	lookups  int
}

func (f *fakeUserStore) LookupUserByEmail(_ context.Context, email string) (domain.User, error) {
	f.lookups++
	f.lookedUp = append(f.lookedUp, email)
	return f.user, f.lookupErr
}

func (f *fakeUserStore) LookupUserByID(_ context.Context, _ string) (domain.User, error) {
	return f.user, f.lookupErr
}

// CreateUser records the user the service built, so tests can assert on
// normalisation, sanitising, and hashing — all of which happen above this line,
// making the recorded value the only place to see whether they ran.
func (f *fakeUserStore) CreateUser(_ context.Context, user domain.User) (domain.User, error) {
	if f.registerErr != nil {
		return domain.User{}, f.registerErr
	}
	f.registered = append(f.registered, user)
	return user, nil
}

func (f *fakeUserStore) EditPassword(_ context.Context, userId, hash string) error {
	f.editCalls++
	f.editedID, f.editedHash = userId, hash
	return f.editErr
}

/* ---------- enforcePasswordComplexity ------------------------------------ */

func TestEnforcePasswordComplexity(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     error
	}{
		{"valid", "NewPass1!", nil},
		{"valid with dollar", "NewPass1$", nil},
		{"valid with tilde", "NewPass1~", nil},
		{"too short", "Ab1!", metrics.ErrInsufficientPasswordLength},
		{"exactly 8 ok", "Abcdefg!", nil},
		{"7 rejected", "Abcdef!", metrics.ErrInsufficientPasswordLength},
		{"no uppercase", "newpass1!", metrics.ErrMissingUppercase},
		{"no special", "NewPassword1", metrics.ErrMissingSpecialCharacters},
		{"digits only", "12345678", metrics.ErrMissingUppercase},
		{"72 bytes ok", strings.Repeat("a", 69) + "A1!", nil},
		{"73 bytes rejected", strings.Repeat("a", 70) + "A1!", metrics.ErrPasswordTooLong},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := enforcePasswordComplexity(tc.password)
			if !errors.Is(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The minimum counts runes but the maximum counts bytes, because bcrypt
// truncates at 72 bytes. A password can therefore be short by one measure and
// over-long by the other.
func TestComplexityLengthUnitsDifferForMultibyte(t *testing.T) {
	// 8 runes, 24 bytes: satisfies the rune minimum.
	if err := enforcePasswordComplexity("Aa1!密码密码"); err != nil {
		t.Errorf("8-rune multibyte password rejected: %v", err)
	}
	// 30 runes but 90 bytes: over bcrypt's byte cap despite being short in runes.
	long := "Aa1!" + strings.Repeat("密", 30)
	if err := enforcePasswordComplexity(long); !errors.Is(err, metrics.ErrPasswordTooLong) {
		t.Errorf("got %v, want metrics.ErrPasswordTooLong (%d runes, %d bytes)",
			err, len([]rune(long)), len(long))
	}
}

/* ---------- ChangePassword ----------------------------------------------- */

func newAuthWithUser(t *testing.T, password string) (*auth, *fakeUserStore) {
	t.Helper()

	hash, err := authen.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeUserStore{user: domain.User{
		ID:           bson.NewObjectID(),
		Email:        "user@example.com",
		Name:         "Test User",
		PasswordHash: hash,
	}}

	// store.Store is a plain struct of interfaces, so the fake drops straight
	// in — no database, no mock framework.
	//
	// The SessionManager gets a nil session store: every test below passes an
	// empty sessionID, which is exactly the branch that skips DeleteSession and
	// therefore never touches Redis. If a future test needs the delete path, it
	// will panic here rather than silently pass, which is the right failure.
	sm := authen.NewSessionManager(
		nil, time.Hour, "jsm_session", "jsm_csrf", "jsm_redirect", 10*time.Minute,
		authen.CookieSettings{}, []byte("test-secret"),
	)
	return NewAuth(&store.Store{AuthenticateStore: fake}, sm, config.MailConfig{}), fake
}

func TestChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	svc, fake := newAuthWithUser(t, "CurrentPass1!")

	_, err := svc.ChangePassword(context.Background(), "uid", "", domain.ChangePasswordRequest{
		CurrentPassword: "NotTheCurrentOne1!",
		NewPassword:     "BrandNewPass1!",
	})
	if !errors.Is(err, metrics.ErrIncorrectCredentials) {
		t.Fatalf("got %v, want metrics.ErrIncorrectCredentials", err)
	}
	// The critical assertion: nothing was written.
	if fake.editCalls != 0 {
		t.Error("password was changed despite the current password being wrong")
	}
}

func TestChangePasswordRejectsWeakNewPassword(t *testing.T) {
	svc, fake := newAuthWithUser(t, "CurrentPass1!")

	_, err := svc.ChangePassword(context.Background(), "uid", "", domain.ChangePasswordRequest{
		CurrentPassword: "CurrentPass1!",
		NewPassword:     "weak",
	})
	if !errors.Is(err, metrics.ErrInsufficientPasswordLength) {
		t.Fatalf("got %v, want metrics.ErrInsufficientPasswordLength", err)
	}
	if fake.editCalls != 0 {
		t.Error("a rejected password was still written to the store")
	}
}

func TestChangePasswordRejectsIdenticalPassword(t *testing.T) {
	svc, fake := newAuthWithUser(t, "CurrentPass1!")

	_, err := svc.ChangePassword(context.Background(), "uid", "", domain.ChangePasswordRequest{
		CurrentPassword: "CurrentPass1!",
		NewPassword:     "CurrentPass1!",
	})
	if !errors.Is(err, metrics.ErrIdenticalPasswords) {
		t.Fatalf("got %v, want metrics.ErrIdenticalPasswords", err)
	}
	if fake.editCalls != 0 {
		t.Error("store was written for a no-op change")
	}
}

func TestChangePasswordPropagatesLookupFailure(t *testing.T) {
	svc, fake := newAuthWithUser(t, "CurrentPass1!")
	fake.lookupErr = metrics.ErrUserNotFound

	_, err := svc.ChangePassword(context.Background(), "uid", "", domain.ChangePasswordRequest{
		CurrentPassword: "CurrentPass1!",
		NewPassword:     "BrandNewPass1!",
	})
	if err == nil {
		t.Fatal("a missing user should not be a silent success")
	}
	if fake.editCalls != 0 {
		t.Error("store was written for a user that could not be loaded")
	}
}

func TestChangePasswordSucceedsAndRotatesHash(t *testing.T) {
	svc, fake := newAuthWithUser(t, "CurrentPass1!")
	oldHash := fake.user.PasswordHash

	cookies, err := svc.ChangePassword(context.Background(), "uid", "", domain.ChangePasswordRequest{
		CurrentPassword: "CurrentPass1!",
		NewPassword:     "BrandNewPass1!",
	})
	if err != nil {
		t.Fatalf("valid change rejected: %v", err)
	}

	if fake.editCalls != 1 {
		t.Fatalf("EditPassword called %d times, want 1", fake.editCalls)
	}
	if fake.editedID != "uid" {
		t.Errorf("wrote against user %q, want %q", fake.editedID, "uid")
	}
	if fake.editedHash == oldHash {
		t.Error("the stored hash did not change")
	}
	// What was written must be a hash of the NEW password, not the old one.
	if !authen.VerifyPassword(fake.editedHash, "BrandNewPass1!") {
		t.Error("stored hash does not verify against the new password")
	}
	if authen.VerifyPassword(fake.editedHash, "CurrentPass1!") {
		t.Error("stored hash still verifies against the old password")
	}

	// Session cookie cleared + CSRF rebound to anonymous. Without the second,
	// the client's next login POST is rejected on a token bound to a dead
	// session.
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2 (session + csrf)", len(cookies))
	}
	var session, csrf *http.Cookie
	for _, c := range cookies {
		switch c.Name {
		case "jsm_session":
			session = c
		case "jsm_csrf":
			csrf = c
		}
	}
	if session == nil || session.MaxAge != -1 || session.Value != "" {
		t.Errorf("session cookie does not clear the session: %+v", session)
	}
	if csrf == nil || csrf.Value == "" {
		t.Fatal("no replacement CSRF cookie issued")
	}
	if !strings.Contains(csrf.Value, "!.") {
		t.Errorf("replacement CSRF token is not bound to the anonymous state: %q", csrf.Value)
	}
}

func TestChangePasswordPropagatesStoreWriteFailure(t *testing.T) {
	svc, fake := newAuthWithUser(t, "CurrentPass1!")
	fake.editErr = errors.New("mongo is down")

	if _, err := svc.ChangePassword(context.Background(), "uid", "", domain.ChangePasswordRequest{
		CurrentPassword: "CurrentPass1!",
		NewPassword:     "BrandNewPass1!",
	}); err == nil {
		t.Fatal("a failed write reported success; the user would think the password changed")
	}
}
