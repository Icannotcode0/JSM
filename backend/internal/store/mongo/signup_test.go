package mongostore

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// testDB gives each test a throwaway database, dropped on cleanup, and skips
// when there is no Mongo to talk to — the store layer's job is translating to
// queries, so a fake driver would only assert that the fake matches the code.
func testDB(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Skipf("no mongo at %s: %v", uri, err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		t.Skipf("no mongo at %s: %v", uri, err)
	}

	db := client.Database("jsm_test_" + bson.NewObjectID().Hex())
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return db
}

// newUserStore returns a store whose users collection carries the unique index
// on email.
//
// Creating the index is not optional: duplicate rejection *is* the index, not
// application logic, so a test that skipped it would prove the opposite of what
// it claims. EnsureIndexes does the same thing at startup.
func newUserStore(t *testing.T) *userStore {
	t.Helper()

	db := testDB(t)
	_, err := db.Collection(metrics.DbuserCollection).Indexes().CreateOne(
		context.Background(),
		mongo.IndexModel{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	)
	if err != nil {
		t.Fatalf("create unique email index: %v", err)
	}
	return NewAuthStore(db)
}

// newUser builds what the service hands down: already validated, already
// hashed. The store does no hashing of its own, so a literal stands in for a
// real bcrypt value here.
func newUser(email string) domain.User {
	return domain.User{
		Email:        email,
		Name:         "Ada Lovelace",
		PasswordHash: "$2a$10$notarealhashbutshapedlikeoneforthistest",
	}
}

func TestCreateUserPersistsTheAccount(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	created, err := s.CreateUser(ctx, newUser("ada@example.com"))
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// The returned document has to be complete: the handler answers 201 with it.
	if created.ID.IsZero() {
		t.Error("no ID was assigned")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("timestamps were not stamped")
	}

	found, err := s.LookupUserByEmail(ctx, "ada@example.com")
	if err != nil {
		t.Fatalf("the account was not readable after creation: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("stored %v, returned %v", found.ID, created.ID)
	}
	if found.Name != "Ada Lovelace" {
		t.Errorf("name = %q", found.Name)
	}
}

// The store writes what it is given and nothing more — it must not silently
// re-hash, blank, or default a field the service already decided.
func TestCreateUserStoresTheGivenHashUnchanged(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	in := newUser("ada@example.com")
	if _, err := s.CreateUser(ctx, in); err != nil {
		t.Fatal(err)
	}

	found, err := s.LookupUserByEmail(ctx, in.Email)
	if err != nil {
		t.Fatal(err)
	}
	if found.PasswordHash != in.PasswordHash {
		t.Errorf("password_hash = %q, want the value handed down unchanged", found.PasswordHash)
	}
}

// Duplicate detection is the unique index rejecting the insert, not a
// read-then-write check — so there is no window between the two for a second
// request to slip through.
func TestCreateUserRejectsDuplicateEmail(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if _, err := s.CreateUser(ctx, newUser("ada@example.com")); err != nil {
		t.Fatal(err)
	}

	_, err := s.CreateUser(ctx, newUser("ada@example.com"))
	if !errors.Is(err, metrics.ErrEmailTaken) {
		t.Fatalf("got %v, want metrics.ErrEmailTaken", err)
	}
}

// The index compares bytes, so whether two spellings collide is decided by the
// normalisation the service applies before calling this. The service now folds
// the whole address, which is what makes these collide.
func TestCreateUserCollidesOnAlreadyNormalisedAddresses(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	if _, err := s.CreateUser(ctx, newUser("ada@example.com")); err != nil {
		t.Fatal(err)
	}
	// Reaching the store un-normalised is a service bug, but the index still
	// treats it as a different address — which is exactly why normalisation
	// has to happen above this layer.
	if _, err := s.CreateUser(ctx, newUser("Ada@example.com")); err != nil {
		t.Fatalf("got %v — the index began folding case, which it does not do", err)
	}
}

// Verification state is written as given rather than defaulted here, so the
// service's policy decision survives the round trip.
func TestCreateUserPreservesVerificationState(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	verifiedAt := time.Now().UTC().Truncate(time.Millisecond)

	unverified := newUser("unverified@example.com")
	verified := newUser("verified@example.com")
	verified.EmailVerified = true
	verified.EmailVerifiedAt = &verifiedAt

	for _, u := range []domain.User{unverified, verified} {
		if _, err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}

	gotUnverified, err := s.LookupUserByEmail(ctx, "unverified@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if gotUnverified.EmailVerified || gotUnverified.EmailVerifiedAt != nil {
		t.Errorf("unverified account came back verified: %+v", gotUnverified)
	}

	gotVerified, err := s.LookupUserByEmail(ctx, "verified@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !gotVerified.EmailVerified {
		t.Error("verified account came back unverified")
	}
	if gotVerified.EmailVerifiedAt == nil {
		t.Fatal("email_verified_at was dropped")
	}
	if !gotVerified.EmailVerifiedAt.Equal(verifiedAt) {
		t.Errorf("email_verified_at = %v, want %v", gotVerified.EmailVerifiedAt, verifiedAt)
	}
}

// A created account has to be reachable by the exact path login uses.
func TestCreateUserIsFoundByBothLookups(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	created, err := s.CreateUser(ctx, newUser("ada@example.com"))
	if err != nil {
		t.Fatal(err)
	}

	byEmail, err := s.LookupUserByEmail(ctx, "ada@example.com")
	if err != nil {
		t.Fatalf("LookupUserByEmail: %v", err)
	}
	byID, err := s.LookupUserByID(ctx, created.ID.Hex())
	if err != nil {
		t.Fatalf("LookupUserByID: %v", err)
	}
	if byID.ID != byEmail.ID || byID.ID != created.ID {
		t.Errorf("lookups disagree: created=%v byEmail=%v byID=%v", created.ID, byEmail.ID, byID.ID)
	}
}
