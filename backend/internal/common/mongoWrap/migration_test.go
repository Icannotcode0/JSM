package mongoWrap

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// EnsureIndexes runs against a database that already holds accounts created
// before email_normalized existed. Those documents have no such field, and the
// new unique index would see every one of them as the same null value and
// reject all but the first — so the backfill has to happen first.
func TestMigrateUserEmailNormalizedBackfillsLegacyAccounts(t *testing.T) {
	db := testDB(t)
	users := db.Collection("users")
	ctx := context.Background()

	// A database as it looked before the split: typed addresses, a unique index
	// on `email`, and no normalized field anywhere.
	if _, err := users.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		t.Fatal(err)
	}
	legacy := []string{"Ada@Example.com", "  bob@EXAMPLE.com  ", "carol@example.com"}
	for _, email := range legacy {
		if _, err := users.InsertOne(ctx, bson.M{"email": email, "name": "Legacy"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := migrateUserEmailNormalized(ctx, users); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Every legacy account now carries a folded form, and the typed one is
	// untouched.
	want := map[string]string{
		"Ada@Example.com":     "ada@example.com",
		"  bob@EXAMPLE.com  ": "bob@example.com",
		"carol@example.com":   "carol@example.com",
	}
	for typed, normalized := range want {
		var got struct {
			Email           string `bson:"email"`
			EmailNormalized string `bson:"email_normalized"`
		}
		if err := users.FindOne(ctx, bson.M{"email": typed}).Decode(&got); err != nil {
			t.Fatalf("%q: %v", typed, err)
		}
		if got.Email != typed {
			t.Errorf("%q: typed form was rewritten to %q", typed, got.Email)
		}
		if got.EmailNormalized != normalized {
			t.Errorf("%q: email_normalized = %q, want %q", typed, got.EmailNormalized, normalized)
		}
	}

	// The superseded constraint is gone, so a second casing of an existing
	// address is no longer blocked by the *old* index — it will be blocked by
	// the new one instead, for the right reason.
	var indexes []struct {
		Name string `bson:"name"`
	}
	cursor, err := users.Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.All(ctx, &indexes); err != nil {
		t.Fatal(err)
	}
	for _, idx := range indexes {
		if idx.Name == legacyUniqueEmailIndex {
			t.Errorf("the superseded %q index is still present", legacyUniqueEmailIndex)
		}
	}
}

// Running twice must be safe: EnsureIndexes fires on every startup.
func TestMigrateUserEmailNormalizedIsIdempotent(t *testing.T) {
	db := testDB(t)
	users := db.Collection("users")
	ctx := context.Background()

	if _, err := users.InsertOne(ctx, bson.M{"email": "Ada@Example.com", "name": "Legacy"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := migrateUserEmailNormalized(ctx, users); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	n, err := users.CountDocuments(ctx, bson.M{"email_normalized": "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("found %d documents, want 1 — the migration duplicated or reprocessed", n)
	}
}

// A database with nothing to migrate must not error.
func TestMigrateUserEmailNormalizedOnEmptyCollection(t *testing.T) {
	db := testDB(t)
	if err := migrateUserEmailNormalized(context.Background(), db.Collection("users")); err != nil {
		t.Errorf("migration on an empty collection failed: %v", err)
	}
}
