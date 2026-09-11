package mongoWrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Sentinel errors live in internal/common/metrics so the store and the service
// share one value — errors.Is then works from the Mongo call to the handler
// with no translation step in between.
var FEmailAddress = "email"

// FindUserByID looks up a user by their Mongo _id (as a hex string).
func FindUserByID(ctx context.Context, collection *mongo.Collection, id string) (domain.User, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return domain.User{}, metrics.ErrUserNotFound
	}

	var user domain.User
	if err := collection.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, metrics.ErrUserNotFound
		}
		return domain.User{}, fmt.Errorf("find user by id: %w", err)
	}
	return user, nil
}

func FindUserByEmail(ctx context.Context, collection *mongo.Collection, email string) (domain.User, error) {
	var user domain.User
	emailFilter := bson.D{{Key: "email", Value: email}}
	if err := collection.FindOne(ctx, emailFilter).Decode(&user); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, metrics.ErrUserNotFound
		}
		return domain.User{}, fmt.Errorf("find user by email: %w", err)
	}
	return user, nil
}

func EditUserPassword(ctx context.Context, collection *mongo.Collection, userId string, passwordHash string) error {
	objID, err := bson.ObjectIDFromHex(userId)
	if err != nil {
		return metrics.ErrUserNotFound
	}

	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "password_hash", Value: passwordHash},
		{Key: "updated_at", Value: time.Now().UTC()},
	}}}

	res, err := collection.UpdateOne(ctx, bson.M{"_id": objID}, update)
	if err != nil {
		return fmt.Errorf("edit user password: %w", err)
	}
	// UpdateOne reports no error when the filter matched nothing, so without
	// this a password change against a deleted user would look like it worked.
	if res.MatchedCount == 0 {
		return metrics.ErrUserNotFound
	}
	return nil
}

// CreateUser inserts a new user, stamping ID/CreatedAt/UpdatedAt.
func CreateUser(ctx context.Context, collection *mongo.Collection, user domain.User) (domain.User, error) {
	now := time.Now().UTC()
	user.ID = bson.NewObjectID()
	user.CreatedAt = now
	user.UpdatedAt = now

	if _, err := collection.InsertOne(ctx, user); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.User{}, metrics.ErrEmailTaken
		}
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}
