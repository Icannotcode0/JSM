package service

import (
	"context"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	authen "github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/config"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
)

// TODO: A validation layer needs to be implemented and injected into corresponding services
// This local helper stays here for now, it will be migrated into the validator of this endpoint later

// metrics.ErrIncorrectCredentials is returned for both "no such user" and "wrong
// password" — never let the caller distinguish which, so a failed login
// never reveals whether a given email is registered.

// Password-policy failures. These are ErrInvalidInput so writeServiceError
// already maps them to 400 with their own message — a user who typed a weak
// password needs to be told which rule they missed, not handed a 500.

// domain names cannot start or end from dashes, at least one dot
var emailDomainPattern = regexp.MustCompile(
	`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?` +
		`(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`,
)

type auth struct {
	store *store.Store
	sm    *authen.SessionManager
	mail  config.MailConfig
}

// maxUserName caps the display name. It is rendered in the topbar and reduced
// to initials, so there is no use for a long one — and without a cap the only
// limit is the 1 MiB request body.
const maxUserName = 100

func NewAuth(store *store.Store, sm *authen.SessionManager, mail config.MailConfig) *auth {
	return &auth{
		store: store,
		sm:    sm,
		mail:  mail,
	}
}

// Authenticate verifies email/password and, on success, creates a session,
// returning the cookies the handler should set — the minimal design: /me is the
// single source of truth for user info, not the login response.
//
// Two cookies come back, not one. The session cookie is the obvious half; the
// CSRF cookie has to be reissued in the same response because tokens are bound
// to a session (see authentication/csrf.go), so the one the client used to
// authorise this very request stops being valid the instant the session exists.
func (a *auth) Authenticate(ctx context.Context, email string, password string) ([]*http.Cookie, error) {

	authLogger := logbuilder.NewDefaultInfoLevelLogger()
	defer func() {
		authLogger.Track("Auth.Service")
	}()

	// Normalised, not validated: a malformed address at login must fail as
	// ordinary bad credentials, not as a distinguishable validation error that
	// would tell a prober which addresses are even well-formed.
	user, err := a.store.AuthenticateStore.LookupUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		// Spend the same time bcrypt would have, so "no such user" and "wrong
		// password" are indistinguishable by duration as well as by message.
		// Without this the two paths differ by ~30x, which enumerates accounts.
		authen.BurnPasswordComparison(password)
		authLogger.Warn("login failed: user lookup error", logbuilder.Fields{"email": email})
		return nil, metrics.ErrIncorrectCredentials
	}

	if !authen.VerifyPassword(user.PasswordHash, password) {
		authLogger.Warn("login failed: password mismatch", logbuilder.Fields{
			"email": email,
			"name":  user.Name,
		})
		return nil, metrics.ErrIncorrectCredentials
	}

	sid, sessionCookie, err := a.sm.CreateSession(ctx, user.ID.Hex(), user.Email, user.Name)
	if err != nil {
		return nil, err
	}

	csrfCookie, err := a.sm.NewCSRFCookie(sid)
	if err != nil {
		return nil, err
	}

	return []*http.Cookie{sessionCookie, csrfCookie}, nil
}

// Logout destroys the session identified by sessionID and returns the cookies
// that put the client back in a signed-out state.
//
// It is deliberately idempotent: an empty or unknown sessionID is not an error.
// API.md specifies that logging out without a session still answers 200, and
// that is the right behaviour — "make sure I am signed out" has succeeded when
// the client holds no session, however it got there. Reporting a failure would
// only invite a retry loop over something already true.
//
// Two cookies come back for the same reason login returns two: CSRF tokens are
// bound to a session, so ending one must hand over a token bound to the
// anonymous state, or the client's next login POST is rejected.
func (a *auth) Logout(ctx context.Context, sessionID string) ([]*http.Cookie, error) {
	authLogger := logbuilder.NewDefaultInfoLevelLogger()

	sessionCookie := a.sm.ClearSessionCookie()

	if sessionID != "" {
		if _, err := a.sm.DeleteSession(ctx, sessionID); err != nil {
			// Clear the client side anyway. A Redis failure here means sessions
			// can't be *validated* either — SessionRequired reads the same
			// store — so nobody is authenticated regardless, and the entry
			// expires on its own TTL. Leaving the user visibly signed in
			// because of a backend hiccup is the worse outcome.
			authLogger.Error("logout: failed to delete session", logbuilder.Fields{
				"error": err.Error(),
			})
		}
	}

	csrfCookie, err := a.sm.NewCSRFCookie("")
	if err != nil {
		return nil, err
	}

	return []*http.Cookie{sessionCookie, csrfCookie}, nil
}

// ChangePassword allows user to change their password, the service layer validates the request, enforces new password format,
// Also calls DB to store the newly set password. Finally, destroy the current session token and creates a new one for the user,
// since this is a protected endpoint, the user have to sign in first to access it, see SessionRequired middleware and Auth endpoint

func (a *auth) ChangePassword(
	ctx context.Context,
	userId string,
	sessionID string,
	req domain.ChangePasswordRequest,
) ([]*http.Cookie, error) {
	logger := logbuilder.NewDefaultInfoLevelLogger()
	defer func() {
		logger.Track("Auth.Service.ChangePassword")
	}()

	// The account comes from the session, never from the body — otherwise this
	// is an arbitrary-user password change wearing a friendly name.
	user, err := a.store.AuthenticateStore.LookupUserByID(ctx, userId)
	if err != nil {
		return nil, err
	}

	// The check this endpoint exists for. Without it, anyone holding a session
	// cookie — a borrowed laptop, a stolen token — can set a password the real
	// owner doesn't know and lock them out permanently.
	if !authen.VerifyPassword(user.PasswordHash, req.CurrentPassword) {
		logger.Warn("change password: current password mismatch", logbuilder.Fields{
			"user_id": userId,
		})
		return nil, metrics.ErrIncorrectCredentials
	}

	if req.CurrentPassword == req.NewPassword {
		return nil, metrics.ErrIdenticalPasswords
	}

	if err := enforcePasswordComplexity(req.NewPassword); err != nil {
		return nil, err
	}

	newPasswordHash, err := authen.HashPassword(req.NewPassword)
	if err != nil {
		return nil, err
	}
	if err := a.store.AuthenticateStore.EditPassword(ctx, userId, newPasswordHash); err != nil {
		return nil, err
	}

	sessionCookie := a.sm.ClearSessionCookie()
	if sessionID != "" {
		if _, err := a.sm.DeleteSession(ctx, sessionID); err != nil {
			logger.Error("change password: failed to delete session", logbuilder.Fields{
				"error": err.Error(),
			})
		}
	}

	// CSRF tokens are bound to a session, so ending one must hand back a token
	// bound to the anonymous state or the client's next login POST is rejected.
	csrfCookie, err := a.sm.NewCSRFCookie("")
	if err != nil {
		return nil, err
	}

	return []*http.Cookie{sessionCookie, csrfCookie}, nil
}

func enforcePasswordComplexity(password string) error {

	const (
		minPasswordRunes = 8
		maxPasswordBytes = 72
	)

	// Two units on purpose. The minimum counts runes, because "8 characters"
	// is a claim about what the user typed. The maximum counts bytes, because
	// bcrypt truncates at 72 *bytes* — 72 runes of CJK is ~216 bytes, and
	// everything past byte 72 would be silently discarded before hashing.
	if utf8.RuneCountInString(password) < minPasswordRunes {
		return metrics.ErrInsufficientPasswordLength
	}
	if len(password) > maxPasswordBytes {
		return metrics.ErrPasswordTooLong
	}

	seenUpper := false
	seenSpecial := false

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			seenUpper = true
		// IsPunct alone is not enough: "$", "+", "<", "=", ">", "^", "|" and "~"
		// are Unicode *symbols*, not punctuation, and rejecting them would look
		// like a bug to anyone whose password contains one.
		case unicode.IsPunct(char), unicode.IsSymbol(char):
			seenSpecial = true
		}
		if seenUpper && seenSpecial {
			break
		}
	}

	if !seenUpper {
		return metrics.ErrMissingUppercase
	}
	if !seenSpecial {
		return metrics.ErrMissingSpecialCharacters
	}
	return nil
}

func (a *auth) CreateUser(ctx context.Context, req domain.SignUpRequest) (domain.User, error) {
	logger := logbuilder.NewDefaultInfoLevelLogger()
	defer func() {
		logger.Track("Service.Authenticate.CreateUser")
	}()

	userEmail, err := ValidateEmail(req.Email)
	if err != nil {
		return domain.User{}, err
	}

	// Same treatment every other stored string gets: trimmed, HTML-escaped, and
	// capped. The name is rendered in the UI and will be read by the extension
	// later, so it is sanitised at write time rather than trusted to whichever
	// consumer remembers to escape it.
	name := clean(req.Name, maxUserName)
	if name == "" {
		return domain.User{}, metrics.Invalid("name is required")
	}

	if err := enforcePasswordComplexity(req.Password); err != nil {
		return domain.User{}, err
	}

	// Hashing belongs here, beside the policy that just validated the password —
	// not in the store, which has no business seeing plaintext.
	hash, err := authen.HashPassword(req.Password)
	if err != nil {
		return domain.User{}, err
	}

	user := domain.User{
		// Both forms: the typed one to show and to send mail to, the folded
		// one to match on.
		Email:           userEmail,
		EmailNormalized: normalizeEmail(userEmail),
		Name:            name,
		PasswordHash:    hash,
	}

	// A local install has no mail transport, so requiring a round-trip the
	// deployment cannot perform would make every account permanently unusable.
	// Verification is therefore only withheld where it can actually be granted.
	if !a.mail.RequireEmailVerification {
		now := time.Now().UTC()
		user.EmailVerified = true
		user.EmailVerifiedAt = &now
	}

	created, err := a.store.AuthenticateStore.CreateUser(ctx, user)
	if err != nil {
		return domain.User{}, err
	}
	return created, nil
}

// normalizeEmail folds an address to the single form the database stores.
//
// Both signup and login must use it. Normalising on write alone is worse than
// not normalising at all, the account is stored as fix@example.com, the login
// looks up FIX@Example.com, no row matches, and the user is told their password
// is wrong for an account they just created.
func normalizeEmail(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func ValidateEmail(input string) (string, error) {
	email := strings.TrimSpace(input)
	if email == "" || len(email) > 254 {
		return "", metrics.ErrInvalidEmail
	}

	// reject non-ASCII characters, internal spaces, quote signs and ctrl signs
	for _, c := range email {
		if c <= ' ' || c >= 127 || c == '"' {
			return "", metrics.ErrInvalidEmail
		}
	}

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", metrics.ErrInvalidEmail
	}

	local, domainName, ok := strings.Cut(email, "@")
	if !ok || len(local) > 64 || !emailDomainPattern.MatchString(domainName) {
		return "", metrics.ErrInvalidEmail
	}

	// Returned as typed, case intact. RFC 5321 makes the local part
	// case-sensitive, so this is the form to display and to send mail to;
	// normalizeEmail produces the separate form used for matching.
	_ = local
	return email, nil
}
