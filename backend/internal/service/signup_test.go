package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	authen "github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/config"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
)

func newSignUpService() (*auth, *fakeUserStore) {
	return newSignUpServiceWith(config.MailConfig{})
}

func newSignUpServiceWith(mail config.MailConfig) (*auth, *fakeUserStore) {
	fake := &fakeUserStore{}
	return NewAuth(&store.Store{AuthenticateStore: fake}, nil, mail), fake
}

/* -------------------------------------------------------------------------
   ValidateEmail
   ------------------------------------------------------------------------- */

func TestValidateEmailAccepts(t *testing.T) {
	for _, in := range []string{
		"a@b.co",
		"ada.lovelace@example.com",
		"ada+jobs@example.co.uk",
		"a_b-c@sub.example.com",
		"123@456.com",
	} {
		t.Run(in, func(t *testing.T) {
			if _, err := ValidateEmail(in); err != nil {
				t.Errorf("rejected a valid address: %v", err)
			}
		})
	}
}

func TestValidateEmailRejects(t *testing.T) {
	cases := map[string]string{
		"empty":                "",
		"whitespace only":      "   ",
		"no at sign":           "notanemail.com",
		"no domain":            "a@",
		"no local part":        "@example.com",
		"no dot in domain":     "a@localhost",
		"domain leading dash":  "a@-example.com",
		"domain trailing dash": "a@example-.com",
		"internal space":       "a b@example.com",
		"display name form":    `"Ada" <ada@example.com>`,
		"angle brackets":       "<ada@example.com>",
		"quote character":      `a"b@example.com`,
		"control character":    "a\x00b@example.com",
		// A *trailing* newline is trimmed and accepted, which is right; an
		// internal one is header-injection shaped and must not be.
		"internal newline":       "a@exam\nple.com",
		"internal tab":           "a@exam\tple.com",
		"non-ascii local":        "adá@example.com",
		"non-ascii domain":       "a@exámple.com",
		"local part over 64":     strings.Repeat("a", 65) + "@example.com",
		"whole address over 254": strings.Repeat("a", 64) + "@" + strings.Repeat("b", 190) + ".com",
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateEmail(in); !errors.Is(err, metrics.ErrInvalidEmail) {
				t.Errorf("accepted %q (err=%v)", in, err)
			}
		})
	}
}

// A display-name address parses fine under mail.ParseAddress, so the exact-match
// guard is the only thing rejecting it. Worth its own test: dropping that guard
// would store "Ada" <ada@example.com> as an address and break every lookup.
func TestValidateEmailRejectsDisplayNameEvenThoughItParses(t *testing.T) {
	if _, err := ValidateEmail(`Ada Lovelace <ada@example.com>`); !errors.Is(err, metrics.ErrInvalidEmail) {
		t.Errorf("a display-name address was accepted: %v", err)
	}
}

// ValidateEmail returns the address as typed. Folding is normalizeEmail's job,
// and the two are deliberately separate: one form is displayed and mailed to,
// the other is matched on.
func TestValidateEmailPreservesTypedCase(t *testing.T) {
	got, err := ValidateEmail("Ada.Lovelace@EXAMPLE.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Ada.Lovelace@EXAMPLE.com" {
		t.Errorf("got %q, want the address exactly as typed", got)
	}
}

func TestNormalizeEmailFoldsEverything(t *testing.T) {
	for _, in := range []string{
		"Ada.Lovelace@Example.com",
		"ADA.LOVELACE@EXAMPLE.COM",
		"  ada.lovelace@example.com  ",
	} {
		if got := NormalizeEmail(in); got != "ada.lovelace@example.com" {
			t.Errorf("%q normalised to %q, want one canonical form", in, got)
		}
	}
}

// Every spelling must reach the store with the same EmailNormalized — that is
// what the unique index collides on — while Email keeps whatever was typed.
func TestCreateUserStoresTypedAndNormalisedForms(t *testing.T) {
	normalised := map[string]bool{}
	typed := map[string]bool{}

	for _, in := range []string{"Ada@Example.com", "ADA@EXAMPLE.COM", "ada@example.com"} {
		svc, fake := newSignUpService()
		req := validSignUp()
		req.Email = in

		if _, err := svc.CreateUser(context.Background(), req); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		stored := fake.registered[0]
		normalised[stored.EmailNormalized] = true
		typed[stored.Email] = true

		if stored.Email != in {
			t.Errorf("%q was stored as %q; the typed casing must survive", in, stored.Email)
		}
	}

	if len(normalised) != 1 {
		t.Errorf("three spellings produced %d normalised forms: %v — the index cannot collide them", len(normalised), normalised)
	}
	if len(typed) != 3 {
		t.Errorf("expected three distinct typed forms, got %v", typed)
	}
}

func TestValidateEmailTrimsSurroundingSpace(t *testing.T) {
	got, err := ValidateEmail("  ada@example.com  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ada@example.com" {
		t.Errorf("got %q, want the address trimmed", got)
	}
}

/* -------------------------------------------------------------------------
   CreateUser
   ------------------------------------------------------------------------- */

func validSignUp() domain.SignUpRequest {
	return domain.SignUpRequest{
		Email:    "ada@example.com",
		Password: "ValidPass1!",
		Name:     "Ada Lovelace",
	}
}

func TestCreateUserSucceeds(t *testing.T) {
	svc, fake := newSignUpService()

	if _, err := svc.CreateUser(context.Background(), validSignUp()); err != nil {
		t.Fatalf("valid signup rejected: %v", err)
	}
	if len(fake.registered) != 1 {
		t.Fatalf("store called %d times, want 1", len(fake.registered))
	}
}

// Validation must happen before the store is touched, or an invalid signup can
// still leave a row behind if the write succeeds before the check runs.
func TestCreateUserRejectsBadEmailWithoutTouchingTheStore(t *testing.T) {
	svc, fake := newSignUpService()

	_, err := svc.CreateUser(context.Background(), domain.SignUpRequest{
		Email: "not-an-email", Password: "ValidPass1!", Name: "Ada",
	})
	if !errors.Is(err, metrics.ErrInvalidEmail) {
		t.Fatalf("got %v, want ErrInvalidEmail", err)
	}
	if len(fake.registered) != 0 {
		t.Error("an invalid email still reached the store")
	}
}

func TestCreateUserEnforcesPasswordPolicy(t *testing.T) {
	cases := map[string]struct {
		password string
		want     error
	}{
		"too short":    {"Ab1!", metrics.ErrInsufficientPasswordLength},
		"no uppercase": {"validpass1!", metrics.ErrMissingUppercase},
		"no special":   {"ValidPass11", metrics.ErrMissingSpecialCharacters},
		"too long":     {strings.Repeat("a", 70) + "A1!", metrics.ErrPasswordTooLong},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, fake := newSignUpService()
			req := validSignUp()
			req.Password = tc.password

			if _, err := svc.CreateUser(context.Background(), req); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
			if len(fake.registered) != 0 {
				t.Error("a rejected password still reached the store")
			}
		})
	}
}

// Signup and change-password must not drift into enforcing different rules —
// they share enforcePasswordComplexity, and this is what keeps that true.
func TestCreateUserAndChangePasswordShareThePolicy(t *testing.T) {
	weak := "weak"

	svc, _ := newSignUpService()
	req := validSignUp()
	req.Password = weak
	_, signUpErr := svc.CreateUser(context.Background(), req)

	policyErr := enforcePasswordComplexity(weak)

	if signUpErr == nil || policyErr == nil || signUpErr.Error() != policyErr.Error() {
		t.Errorf("signup=%v policy=%v — the two paths disagree", signUpErr, policyErr)
	}
}

// Surrounding whitespace is stripped from both forms — it is never meaningful,
// and leaving it in the typed field would display an address with a stray space.
func TestCreateUserTrimsBeforeStoring(t *testing.T) {
	svc, fake := newSignUpService()

	req := validSignUp()
	req.Email = "  Ada@EXAMPLE.COM  "

	if _, err := svc.CreateUser(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	stored := fake.registered[0]
	if stored.Email != "Ada@EXAMPLE.COM" {
		t.Errorf("Email = %q, want it trimmed but otherwise untouched", stored.Email)
	}
	if stored.EmailNormalized != "ada@example.com" {
		t.Errorf("EmailNormalized = %q", stored.EmailNormalized)
	}
}

// A taken address is the unique index rejecting the insert, not a pre-check —
// so the error has to survive the trip back up unchanged for the handler to
// turn it into a 409.
func TestCreateUserPropagatesEmailTaken(t *testing.T) {
	svc, fake := newSignUpService()
	fake.registerErr = metrics.ErrEmailTaken

	if _, err := svc.CreateUser(context.Background(), validSignUp()); !errors.Is(err, metrics.ErrEmailTaken) {
		t.Fatalf("got %v, want ErrEmailTaken", err)
	}
}

func TestCreateUserPropagatesStoreFailure(t *testing.T) {
	svc, fake := newSignUpService()
	boom := errors.New("mongo is down")
	fake.registerErr = boom

	if _, err := svc.CreateUser(context.Background(), validSignUp()); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the store's error", err)
	}
}

/* -------------------------------------------------------------------------
   Hashing, sanitising, and verification policy

   All three moved up from the store, so this is where they are pinned now.
   ------------------------------------------------------------------------- */

// Hashing belongs beside the policy that validated the password. The store must
// never see plaintext, and the value it does see has to verify.
func TestCreateUserHashesThePasswordBeforeTheStore(t *testing.T) {
	svc, fake := newSignUpService()
	req := validSignUp()

	user, err := svc.CreateUser(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	stored := fake.registered[0]
	if stored.PasswordHash == req.Password {
		t.Fatal("the plaintext password was handed to the store")
	}
	if !strings.HasPrefix(stored.PasswordHash, "$2") {
		t.Errorf("password_hash = %q, want a bcrypt hash", stored.PasswordHash)
	}
	if !authen.VerifyPassword(stored.PasswordHash, req.Password) {
		t.Error("the hash does not verify against the submitted password")
	}
	if authen.VerifyPassword(stored.PasswordHash, "SomethingElse1!") {
		t.Error("the hash verifies against the wrong password")
	}
	if user.PasswordHash != stored.PasswordHash {
		t.Error("the returned user disagrees with what was stored")
	}
}

// Two accounts with the same password must not share a hash, or a database leak
// reveals which accounts match.
func TestCreateUserSaltsEachHash(t *testing.T) {
	svc, fake := newSignUpService()

	for _, email := range []string{"one@example.com", "two@example.com"} {
		req := validSignUp()
		req.Email = email
		if _, err := svc.CreateUser(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}

	if fake.registered[0].PasswordHash == fake.registered[1].PasswordHash {
		t.Error("identical passwords produced identical hashes; the salt is missing")
	}
}

// The name is rendered in the UI and will be read by the extension later, so it
// gets the same treatment as every other stored string.
func TestCreateUserSanitisesTheName(t *testing.T) {
	svc, fake := newSignUpService()

	req := validSignUp()
	req.Name = "  <script>alert(1)</script>Ada  "

	if _, err := svc.CreateUser(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	got := fake.registered[0].Name
	if strings.Contains(got, "<script>") {
		t.Errorf("HTML stored raw: %q", got)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Errorf("name not trimmed: %q", got)
	}
}

func TestCreateUserCapsTheName(t *testing.T) {
	svc, fake := newSignUpService()

	req := validSignUp()
	req.Name = strings.Repeat("a", 5000)

	if _, err := svc.CreateUser(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.registered[0].Name); n > maxUserName {
		t.Errorf("name stored at %d characters, want at most %d", n, maxUserName)
	}
}

func TestCreateUserRequiresAName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		svc, fake := newSignUpService()
		req := validSignUp()
		req.Name = name

		var invalid metrics.InvalidInputError
		if _, err := svc.CreateUser(context.Background(), req); !errors.As(err, &invalid) {
			t.Errorf("name %q: got %v, want an invalid-input error", name, err)
		}
		if len(fake.registered) != 0 {
			t.Error("a nameless signup still reached the store")
		}
	}
}

// A local install has no mail transport, so requiring a verification round-trip
// it cannot perform would make every account permanently unusable. Verification
// is withheld only where it can actually be granted.
func TestCreateUserHonoursTheVerificationPolicy(t *testing.T) {
	t.Run("local install marks verified", func(t *testing.T) {
		svc, fake := newSignUpServiceWith(config.MailConfig{RequireEmailVerification: false})

		user, err := svc.CreateUser(context.Background(), validSignUp())
		if err != nil {
			t.Fatal(err)
		}
		if !fake.registered[0].EmailVerified {
			t.Error("a local signup was left unverified with no way to verify it")
		}
		if fake.registered[0].EmailVerifiedAt == nil {
			t.Error("email_verified_at was not stamped alongside the flag")
		}
		if !user.EmailVerified {
			t.Error("the returned user disagrees with what was stored")
		}
	})

	t.Run("hosted install withholds verification", func(t *testing.T) {
		svc, fake := newSignUpServiceWith(config.MailConfig{RequireEmailVerification: true})

		if _, err := svc.CreateUser(context.Background(), validSignUp()); err != nil {
			t.Fatal(err)
		}
		if fake.registered[0].EmailVerified {
			t.Error("verification was required but the account was created verified")
		}
		if fake.registered[0].EmailVerifiedAt != nil {
			t.Error("email_verified_at was stamped on an unverified account")
		}
	})
}

// The handler answers 201 with this, so it has to be the stored document — not
// the request echoed back.
func TestCreateUserReturnsTheStoredUser(t *testing.T) {
	svc, fake := newSignUpService()

	req := validSignUp()
	req.Email = "ADA@Example.com"

	user, err := svc.CreateUser(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "ADA@Example.com" {
		t.Errorf("returned email = %q, want the typed form", user.Email)
	}
	if user.EmailNormalized != "ada@example.com" {
		t.Errorf("returned EmailNormalized = %q", user.EmailNormalized)
	}
	if user.Email != fake.registered[0].Email {
		t.Error("the returned user disagrees with what was stored")
	}
}

// Signup and login must agree on the canonical form, or an account is created
// under one spelling and unreachable under another. Normalising on write alone
// is worse than not normalising: it produces an account the user cannot log
// into with the address they typed.
//
// Asserted on the address the store is *queried* with, because the real lookup
// is an exact match — if login queries a spelling signup did not store, no row
// comes back and the user is told their password is wrong.
func TestSignUpAndLoginAgreeOnTheCanonicalAddress(t *testing.T) {
	svc, fake := newSignUpService()

	req := validSignUp()
	req.Email = "Ada@Example.COM"
	created, err := svc.CreateUser(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Make every lookup miss, so Authenticate returns before it reaches session
	// creation — this test is about the query, not the session.
	fake.lookupErr = metrics.ErrUserNotFound
	fake.lookedUp = nil

	for _, spelling := range []string{
		"Ada@Example.COM",
		"ada@example.com",
		"ADA@EXAMPLE.COM",
		"  ada@example.com  ",
	} {
		if _, err := svc.Authenticate(context.Background(), spelling, req.Password); !errors.Is(err, metrics.ErrIncorrectCredentials) {
			t.Fatalf("%q: unexpected error %v", spelling, err)
		}
	}

	for i, got := range fake.lookedUp {
		if got != created.EmailNormalized {
			t.Errorf("login queried %q but signup stored EmailNormalized %q (case %d)",
				got, created.EmailNormalized, i)
		}
	}
}
