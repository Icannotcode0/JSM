package mongoWrap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

var (
	// Unique: one account per address, compared case-insensitively. The
	// constraint is on the normalized form, not the typed one — users.email
	// preserves whatever casing the user entered and is deliberately
	// unconstrained. This is the only unique index in the schema.
	userCollectionIndex = bson.D{{Key: "email_normalized", Value: 1}}

	// Base filter for every application query — every list/get/patch/delete
	// goes through user_id (see DATABASE.md, "Multi-tenancy").
	applicationCollectionUserIdIndex = bson.D{{Key: "user_id", Value: 1}}

	// Serves GET /applications?status= directly.
	applicationCollectionUserIdStatusIndex = bson.D{
		{Key: "user_id", Value: 1},
		{Key: "status", Value: 1},
	}

	// Search (GET /applications?q=) matches on these fields. DATABASE.md
	// deferred this index "until search is a real feature being built" — it
	// now is.
	applicationCollectionSearchIndex = bson.D{
		{Key: "user_id", Value: 1},
		{Key: "company_name", Value: 1},
		{Key: "position_title", Value: 1},
	}
)

type MongoClient struct {
	Client          *mongo.Client
	DB              *mongo.Database
	UsersCollection string
	AppsCollection  string
}

// NewClient initializes a new MongoClient instance by reading config from .env

func NewClient(ctx context.Context, uri, dbName, usersCollection, appsCollection string) (*MongoClient, error) {

	newMongoClientOpts := options.Client().ApplyURI(uri)
	newMongoClient, err := mongo.Connect(newMongoClientOpts)
	if err != nil {
		return nil, err
	}

	if err := newMongoClient.Ping(ctx, readpref.Primary()); err != nil {
		_ = newMongoClient.Disconnect(context.Background())
		return nil, err
	}

	return &MongoClient{
		Client:          newMongoClient,
		DB:              newMongoClient.Database(dbName),
		UsersCollection: usersCollection,
		AppsCollection:  appsCollection,
	}, nil
}

func (m *MongoClient) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.Client.Disconnect(ctx)
}

// EnsureIndexes creates the indexes JSM depends on. Safe to call every
// startup — CreateOne is a no-op if the index already exists.
//
// Only the users.email index is unique. The application indexes are lookup
// paths, not constraints: a user has many applications per status, so a unique
// index on {user_id, status} would reject every application after the first in
// each column.
func (m *MongoClient) EnsureIndexes(ctx context.Context) error {
	apps := m.DB.Collection(m.AppsCollection)

	// Drop the unique {user_id, status} index created by an earlier version.
	// CreateOne will not alter an existing index's options, so without this an
	// already-provisioned database keeps the constraint forever and every
	// second application in a status fails with a duplicate-key error.
	if err := dropLegacyUniqueAppIndex(ctx, apps); err != nil {
		return err
	}

	users := m.DB.Collection(m.UsersCollection)
	if err := migrateUserEmailNormalized(ctx, users); err != nil {
		return err
	}

	specs := []struct {
		collection *mongo.Collection
		model      mongo.IndexModel
	}{
		{m.DB.Collection(m.UsersCollection), mongo.IndexModel{
			Keys:    userCollectionIndex,
			Options: options.Index().SetUnique(true),
		}},
		{apps, mongo.IndexModel{Keys: applicationCollectionUserIdIndex}},
		{apps, mongo.IndexModel{Keys: applicationCollectionUserIdStatusIndex}},
		{apps, mongo.IndexModel{Keys: applicationCollectionSearchIndex}},
	}

	errMsg := []string{}
	for _, spec := range specs {
		if _, err := spec.collection.Indexes().CreateOne(ctx, spec.model); err != nil {
			errMsg = append(errMsg, err.Error())
		}
	}
	if len(errMsg) > 0 {
		return errors.New(strings.Join(errMsg, ","))
	}

	return nil
}

// migrateUserEmailNormalized backfills email_normalized on accounts created
// before the field existed, then drops the unique index that used to sit on
// users.email.
//
// Order matters. Backfill first: creating the new unique index on a collection
// where every document has a missing email_normalized would see them all as the
// same null value and reject every document after the first. Drop second, so a
// crash between the two leaves the old constraint protecting the collection
// rather than nothing at all.
func migrateUserEmailNormalized(ctx context.Context, users *mongo.Collection) error {
	cursor, err := users.Find(ctx, bson.M{"email_normalized": bson.M{"$exists": false}})
	if err != nil {
		return fmt.Errorf("find users needing email_normalized: %w", err)
	}

	var legacy []struct {
		ID    bson.ObjectID `bson:"_id"`
		Email string        `bson:"email"`
	}
	if err := cursor.All(ctx, &legacy); err != nil {
		return fmt.Errorf("read users needing email_normalized: %w", err)
	}

	for _, u := range legacy {
		_, err := users.UpdateByID(ctx, u.ID, bson.M{
			"$set": bson.M{"email_normalized": strings.ToLower(strings.TrimSpace(u.Email))},
		})
		if err != nil {
			return fmt.Errorf("backfill email_normalized for %s: %w", u.ID.Hex(), err)
		}
	}
	if len(legacy) > 0 {
		log.Printf("mongoWrap: backfilled email_normalized on %d user(s)", len(legacy))
	}

	return dropIndexIfPresent(ctx, users, legacyUniqueEmailIndex)
}

// dropIndexIfPresent removes an index by name, tolerating its absence.
func dropIndexIfPresent(ctx context.Context, col *mongo.Collection, name string) error {
	cursor, err := col.Indexes().List(ctx)
	if err != nil {
		return fmt.Errorf("list %s indexes: %w", col.Name(), err)
	}
	var existing []struct {
		Name string `bson:"name"`
	}
	if err := cursor.All(ctx, &existing); err != nil {
		return fmt.Errorf("read %s indexes: %w", col.Name(), err)
	}

	for _, idx := range existing {
		if idx.Name != name {
			continue
		}
		if err := col.Indexes().DropOne(ctx, name); err != nil {
			return fmt.Errorf("drop index %s: %w", name, err)
		}
		log.Printf("mongoWrap: dropped superseded index %q on %s", name, col.Name())
	}
	return nil
}

// legacyUniqueEmailIndex is the name Mongo generated for the unique index that
// used to sit on users.email, before matching moved to email_normalized.
const legacyUniqueEmailIndex = "email_1"

// legacyUniqueAppIndex is the name Mongo generated for the incorrect unique
// index on {user_id, status}.
const legacyUniqueAppIndex = "user_id_1_status_1"

func dropLegacyUniqueAppIndex(ctx context.Context, apps *mongo.Collection) error {
	cursor, err := apps.Indexes().List(ctx)
	if err != nil {
		return fmt.Errorf("list application indexes: %w", err)
	}

	var existing []struct {
		Name   string `bson:"name"`
		Unique bool   `bson:"unique"`
	}
	if err := cursor.All(ctx, &existing); err != nil {
		return fmt.Errorf("read application indexes: %w", err)
	}

	for _, idx := range existing {
		// Only drop it when it carries the unique flag — the non-unique index
		// of the same name is the one we want and must be left alone.
		if idx.Name == legacyUniqueAppIndex && idx.Unique {
			if err := apps.Indexes().DropOne(ctx, idx.Name); err != nil {
				return fmt.Errorf("drop legacy unique index %s: %w", idx.Name, err)
			}
			log.Printf("mongoWrap: dropped incorrect unique index %q on %s", idx.Name, apps.Name())
		}
	}
	return nil
}
