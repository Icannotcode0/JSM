// Package metrics holds the values shared across layers that have no behaviour
// of their own: wire-format error codes, sentinel errors, and collection names.
//
// It imports nothing from the rest of the tree and never will — that is what
// lets every layer reference it without creating a cycle.
package metrics

import (
	"errors"
	"fmt"
)

const (
	FCSRFTOKEN = "X-CSRF-TOKEN"
)

const (
	DbuserCollection        = "users"
	DbApplicationCollection = "applications"
)

// ---------------------------------------------------------------------------
// Wire codes
//
// What the client actually receives in {"error": "..."}. Machine-readable and
// stable: the frontend maps them to prose in ERROR_COPY, so changing one is a
// breaking API change, not a wording tweak.
//
// Named Code* rather than Err* so they cannot be confused with the sentinel
// errors below. They are strings, not errors, and the two were previously
// indistinguishable by name.
// ---------------------------------------------------------------------------

const (
	CodeInternalServerError  = "INTERNAL_SERVER_ERROR"
	CodeBadRequest           = "BAD_REQUEST"
	CodeAuthenticationFailed = "AUTHENTICATION_FAILED"
	CodeIncorrectCredentials = "INCORRECT_CREDENTIALS"
	CodeServiceUnavailable   = "SERVICE_UNAVAILABLE"
	CodeRequestTooLarge      = "REQUEST_TOO_LARGE"
	CodeUnauthorized         = "UNAUTHORIZED"
	CodeNotFound             = "NOT_FOUND"
	CodeIdenticalCredentials = "IDENTICAL_CREDENTIALS"
	CodeInvalidEmail         = "INVALID_EMAIL_ADDRESS"
	CodeEmailAlreadyTaken    = "EMAIL_ALREADY_REGISTERED"
	CodeTooManyRequests      = "TOO_MANY_REQUESTS"
)

// ---------------------------------------------------------------------------
// Sentinel errors
//
// One definition each, shared by every layer that raises or inspects them.
//
// These used to be declared twice — once in the store and again in the service,
// as distinct values — with a translation function in between. That indirection
// bought layer independence in theory and cost a mapping step plus a second
// place to look in practice. A single value means errors.Is works from the
// Mongo call all the way up to the handler with nothing in between.
// ---------------------------------------------------------------------------

var (
	// ErrUserNotFound covers both "no such user" and a session naming a user
	// that has since been deleted.
	ErrUserNotFound = errors.New("user not found")

	// ErrEmailTaken is the unique index on users.email rejecting a duplicate.
	ErrEmailTaken = errors.New("email already registered")

	// ErrIncorrectCredentials is returned for "no such user" and "wrong
	// password" alike — never let a caller distinguish them, or a failed login
	// reveals which addresses are registered.
	ErrIncorrectCredentials = errors.New(CodeIncorrectCredentials)

	// ErrApplicationNotFound covers "no such application" and "belongs to
	// someone else" alike. The store cannot tell them apart by design, because
	// every query is scoped by user_id in the filter itself.
	ErrApplicationNotFound = errors.New("application not found")
)

// ErrInvalidEmail is a validation failure, so it carries a caller-facing reason
// rather than being an opaque sentinel — the client can act on "that isn't a
// valid address" in a way it cannot act on a 500.
var ErrInvalidEmail = InvalidInputError{Reason: "a valid email address is required"}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// InvalidInputError carries a caller-facing reason.
//
// It is the only error whose message is returned to the client verbatim, which
// is safe precisely because every Reason is a literal written in this codebase
// — never a database or driver string interpolated into one.
type InvalidInputError struct{ Reason string }

func (e InvalidInputError) Error() string { return e.Reason }

// Invalid builds an InvalidInputError with a formatted reason.
func Invalid(format string, args ...any) error {
	return InvalidInputError{Reason: fmt.Sprintf(format, args...)}
}

// Password-policy failures. Declared here so signup and change-password cannot
// drift into enforcing different rules.
var (
	ErrInsufficientPasswordLength = InvalidInputError{Reason: "password must be at least 8 characters"}
	ErrPasswordTooLong            = InvalidInputError{Reason: "password must be at most 72 bytes"}
	ErrMissingUppercase           = InvalidInputError{Reason: "password must contain an uppercase letter"}
	ErrMissingSpecialCharacters   = InvalidInputError{Reason: "password must contain a special character"}
	ErrIdenticalPasswords         = InvalidInputError{Reason: "new password must differ from the current one"}
)
