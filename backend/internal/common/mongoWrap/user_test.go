package mongoWrap

import (
	"context"
	"testing"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func seedUser(t *testing.T, col *mongo.Collection, email string) domain.User {
	t.Helper()
	user, err := CreateUser(context.Background(), col, domain.User{
		Email:        email,
		Name:         "Test User",
		PasswordHash: "$2a$10$originalhashoriginalhashoriginalhashoriginalhashoriginalha",
	})
	if err != nil {
		t.Fatalf("seed %s: %v", email, err)
	}
	return user
}

func TestFindUserByIDAndEmail(t *testing.T) {
	col := testDB(t).Collection("users")
	ctx := context.Background()
	user := seedUser(t, col, "found@example.com")

	t.Run("by id", func(t *testing.T) {
		got, err := FindUserByID(ctx, col, user.ID.Hex())
		if err != nil {
			t.Fatal(err)
		}
		if got.Email != "found@example.com" {
			t.Errorf("email = %q", got.Email)
		}
	})

	t.Run("by email", func(t *testing.T) {
		got, err := FindUserByEmail(ctx, col, "found@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != user.ID {
			t.Errorf("id = %v, want %v", got.ID, user.ID)
		}
	})

	t.Run("missing id", func(t *testing.T) {
		if _, err := FindUserByID(ctx, col, bson.NewObjectID().Hex()); err != ErrUserNotFound {
			t.Errorf("got %v, want ErrUserNotFound", err)
		}
	})

	// A malformed ID and an absent one are the same fact to a caller.
	t.Run("malformed id", func(t *testing.T) {
		if _, err := FindUserByID(ctx, col, "not-an-objectid"); err != ErrUserNotFound {
			t.Errorf("got %v, want ErrUserNotFound", err)
		}
	})

	t.Run("missing email", func(t *testing.T) {
		if _, err := FindUserByEmail(ctx, col, "nobody@example.com"); err != ErrUserNotFound {
			t.Errorf("got %v, want ErrUserNotFound", err)
		}
	})
}

func TestEditUserPassword(t *testing.T) {
	col := testDB(t).Collection("users")
	ctx := context.Background()
	seedUser(t, col, "rotate@example.com")

	// Read the seeded user back rather than using the value CreateUser
	// returned. BSON stores dates at millisecond precision while Go's
	// time.Time is nanosecond, so the in-memory value is never equal to the
	// stored one — comparing the two would fail on ~1ms of truncation and look
	// like the field had been modified.
	user, err := FindUserByEmail(ctx, col, "rotate@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Sleep so a changed updated_at is distinguishable at that resolution.
	time.Sleep(5 * time.Millisecond)

	if err := EditUserPassword(ctx, col, user.ID.Hex(), "$2a$10$newhash"); err != nil {
		t.Fatal(err)
	}

	got, err := FindUserByID(ctx, col, user.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != "$2a$10$newhash" {
		t.Errorf("hash = %q, want the new one", got.PasswordHash)
	}
	if !got.UpdatedAt.After(user.UpdatedAt) {
		t.Errorf("updated_at not advanced: %v vs %v", got.UpdatedAt, user.UpdatedAt)
	}
	if !got.CreatedAt.Equal(user.CreatedAt) {
		t.Error("created_at was modified")
	}
	if got.Email != "rotate@example.com" || got.Name != "Test User" {
		t.Error("an unrelated field was overwritten")
	}
}

// UpdateOne reports no error when the filter matched nothing, so without the
// MatchedCount check a rotation against a deleted user looks like it worked.
func TestEditUserPasswordOnMissingUser(t *testing.T) {
	col := testDB(t).Collection("users")
	ctx := context.Background()

	if err := EditUserPassword(ctx, col, bson.NewObjectID().Hex(), "$2a$10$x"); err != ErrUserNotFound {
		t.Errorf("absent user: got %v, want ErrUserNotFound", err)
	}
	if err := EditUserPassword(ctx, col, "not-an-objectid", "$2a$10$x"); err != ErrUserNotFound {
		t.Errorf("malformed id: got %v, want ErrUserNotFound", err)
	}
}

func TestCreateUserRejectsDuplicateEmail(t *testing.T) {
	db := testDB(t)
	col := db.Collection("users")
	ctx := context.Background()

	// The uniqueness guarantee is the index, not application logic — so the
	// test has to create it, exactly as EnsureIndexes does at startup.
	if _, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: newUniqueIndexOptions(),
	}); err != nil {
		t.Fatal(err)
	}

	seedUser(t, col, "dupe@example.com")
	if _, err := CreateUser(ctx, col, domain.User{Email: "dupe@example.com", Name: "Impostor"}); err != ErrEmailTaken {
		t.Errorf("got %v, want ErrEmailTaken", err)
	}
}

func newUniqueIndexOptions() *options.IndexOptionsBuilder {
	return options.Index().SetUnique(true)
}
