package mongostore

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type health struct {
	db *mongo.Database
}

func NewHealth(db *mongo.Database) *health {
	return &health{
		db: db,
	}
}

func (h *health) CheckHealth(ctx context.Context) error {
	return h.db.Client().Ping(ctx, nil)
}
