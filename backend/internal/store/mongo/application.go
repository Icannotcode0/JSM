package mongostore

import (
	"context"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/mongoWrap"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type applicationStore struct {
	db *mongo.Database
}

func NewApplicationStore(db *mongo.Database) *applicationStore {
	return &applicationStore{db: db}
}

func (s *applicationStore) collection() *mongo.Collection {
	return s.db.Collection(metrics.DbApplicationCollection)
}

func (s *applicationStore) Create(ctx context.Context, app domain.Application) (domain.Application, error) {
	return mongoWrap.CreateApplication(ctx, s.collection(), app)
}

func (s *applicationStore) Get(ctx context.Context, id, userID bson.ObjectID) (domain.Application, error) {
	return mongoWrap.FindApplicationByID(ctx, s.collection(), id, userID)
}

func (s *applicationStore) List(
	ctx context.Context,
	userID bson.ObjectID,
	query domain.ListApplicationsQuery,
) ([]domain.Application, int64, error) {
	return mongoWrap.ListApplications(ctx, s.collection(), userID, query)
}

func (s *applicationStore) Update(
	ctx context.Context,
	id, userID bson.ObjectID,
	set bson.M,
) (domain.Application, error) {
	return mongoWrap.UpdateApplication(ctx, s.collection(), id, userID, set)
}

func (s *applicationStore) Delete(ctx context.Context, id, userID bson.ObjectID) error {
	return mongoWrap.DeleteApplication(ctx, s.collection(), id, userID)
}
