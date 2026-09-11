package store

import (
	"context"

	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	mongostore "github.com/Icannotcode0/job-app-manager/backend/internal/store/mongo"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// HealthCheck pings the underlying database.
type HealthCheck interface {
	CheckHealth(ctx context.Context) error
}

// Authenticator reads the user records login and GET /me are served from.
type Authenticator interface {
	LookupUserByEmail(ctx context.Context, email string) (domain.User, error)
	LookupUserByID(ctx context.Context, id string) (domain.User, error)
	EditPassword(ctx context.Context, userId string, password string) error
	CreateUser(ctx context.Context, user domain.User) (domain.User, error)
}

// Applications is the persistence surface for job applications.
//
// Every method takes userID and the implementation puts it in the query filter
// rather than checking ownership after reading, so no code path here can reach
// another user's document (DATABASE.md, "Multi-tenancy").
type Applications interface {
	Create(ctx context.Context, app domain.Application) (domain.Application, error)
	Delete(ctx context.Context, id, userID bson.ObjectID) error
	Get(ctx context.Context, id, userID bson.ObjectID) (domain.Application, error)
	List(ctx context.Context, userID bson.ObjectID, query domain.ListApplicationsQuery) ([]domain.Application, int64, error)
	Update(ctx context.Context, id, userID bson.ObjectID, set bson.M) (domain.Application, error)
}

// Store is the aggregate of every persistence capability, keyed by what it
// does rather than by which database backs it — swapping a backend means
// changing NewStore only.
type Store struct {
	Health            HealthCheck
	AuthenticateStore Authenticator
	Applications      Applications
}

// NewStore wires the Mongo-backed implementations.
//
// It takes only *mongo.Database: the previous *mongo.Client parameter was
// never used, and a Database already exposes its client via Database.Client()
// for the ping in the health check.
func NewStore(database *mongo.Database) *Store {
	return &Store{
		Health:            mongostore.NewHealth(database),
		AuthenticateStore: mongostore.NewAuthStore(database),
		Applications:      mongostore.NewApplicationStore(database),
	}
}
