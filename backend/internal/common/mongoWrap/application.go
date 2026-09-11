package mongoWrap

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// scopeFilter is the only way this file builds a filter for a single document.
//
// Every application query is scoped by user_id *in the filter*, never checked
// after the fact in the handler — that's what makes leaking another user's
// document structurally impossible rather than a check someone can forget
// (DATABASE.md, "Multi-tenancy"). A document that exists but belongs to someone
// else simply doesn't match, so it returns metrics.ErrApplicationNotFound exactly like
// one that was never there, and the caller can't tell the difference.
func scopeFilter(id, userID bson.ObjectID) bson.M {
	return bson.M{"_id": id, "user_id": userID}
}

// CreateApplication inserts app, stamping ID and timestamps.
func CreateApplication(ctx context.Context, collection *mongo.Collection, app domain.Application) (domain.Application, error) {
	now := time.Now().UTC()
	app.ID = bson.NewObjectID()
	app.CreatedAt = now
	app.UpdatedAt = now

	if _, err := collection.InsertOne(ctx, app); err != nil {
		return domain.Application{}, fmt.Errorf("create application: %w", err)
	}
	return app, nil
}

// FindApplicationByID returns one application belonging to userID.
func FindApplicationByID(ctx context.Context, collection *mongo.Collection, id, userID bson.ObjectID) (domain.Application, error) {
	var app domain.Application
	err := collection.FindOne(ctx, scopeFilter(id, userID)).Decode(&app)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Application{}, metrics.ErrApplicationNotFound
		}
		return domain.Application{}, fmt.Errorf("find application: %w", err)
	}
	return app, nil
}

// ListApplications returns one page of applications plus the total matching the
// same filter (not the page), so the caller can report how many exist overall.
func ListApplications(
	ctx context.Context,
	collection *mongo.Collection,
	userID bson.ObjectID,
	query domain.ListApplicationsQuery,
) ([]domain.Application, int64, error) {
	filter := listFilter(userID, query)

	total, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("count applications: %w", err)
	}

	opts := options.Find().
		// Newest application first; _id breaks ties so paging is stable when
		// several share an applied_at (a bulk import gives them all the same
		// timestamp, and an unstable sort would repeat or skip rows).
		SetSort(bson.D{{Key: "applied_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((query.Page - 1) * query.PageSize)).
		SetLimit(int64(query.PageSize))

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("list applications: %w", err)
	}

	// Non-nil so an empty page marshals as [] rather than null — the frontend
	// types every collection as an array.
	apps := []domain.Application{}
	if err := cursor.All(ctx, &apps); err != nil {
		return nil, 0, fmt.Errorf("decode applications: %w", err)
	}
	return apps, total, nil
}

func listFilter(userID bson.ObjectID, query domain.ListApplicationsQuery) bson.M {
	filter := bson.M{"user_id": userID}

	if query.Status != "" {
		filter["status"] = query.Status
	}
	if query.Tag != "" {
		// Matching a scalar against an array field tests membership in Mongo.
		filter["tags"] = query.Tag
	}
	if query.Search != "" {
		// QuoteMeta is not optional: this string comes straight from a query
		// parameter, and an unescaped one would let a caller inject regex
		// syntax — at best matching documents they didn't intend, at worst
		// pinning a CPU on a catastrophically backtracking pattern.
		pattern := regexp.QuoteMeta(query.Search)
		like := bson.M{"$regex": pattern, "$options": "i"}
		filter["$or"] = []bson.M{
			{"company_name": like},
			{"position_title": like},
			{"tags": like},
			{"notes": like},
		}
	}
	return filter
}

// UpdateApplication applies set to one application owned by userID and returns
// the updated document.
func UpdateApplication(
	ctx context.Context,
	collection *mongo.Collection,
	id, userID bson.ObjectID,
	set bson.M,
) (domain.Application, error) {
	if len(set) == 0 {
		// Nothing to change — return the current document rather than issuing
		// an empty $set, which Mongo rejects.
		return FindApplicationByID(ctx, collection, id, userID)
	}
	set["updated_at"] = time.Now().UTC()

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	var app domain.Application
	err := collection.
		FindOneAndUpdate(ctx, scopeFilter(id, userID), bson.M{"$set": set}, opts).
		Decode(&app)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Application{}, metrics.ErrApplicationNotFound
		}
		return domain.Application{}, fmt.Errorf("update application: %w", err)
	}
	return app, nil
}

// DeleteApplication removes one application owned by userID.
func DeleteApplication(ctx context.Context, collection *mongo.Collection, id, userID bson.ObjectID) error {
	res, err := collection.DeleteOne(ctx, scopeFilter(id, userID))
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	if res.DeletedCount == 0 {
		return metrics.ErrApplicationNotFound
	}
	return nil
}
