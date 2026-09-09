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
	// Unique: one account per email address. This is the only unique index in
	// the schema.
	userCollectionIndex = bson.D{{Key: "email", Value: 1}}

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
