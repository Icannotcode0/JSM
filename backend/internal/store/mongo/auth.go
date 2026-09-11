package mongostore

import (
	"context"
	"fmt"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/mongoWrap"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	userCollectionName = "users"
)

type userStore struct {
	db *mongo.Database
}

func NewAuthStore(db *mongo.Database) *userStore {
	return &userStore{db: db}
}

func (s *userStore) LookupUserByEmail(ctx context.Context, email string) (domain.User, error) {
	user, err := mongoWrap.FindUserByEmail(ctx, s.db.Collection(userCollectionName), email)
	if err != nil {
		return domain.User{}, fmt.Errorf("find user by email: %w", err)
	}
	return user, nil
}

// LookupUserByID backs GET /me: the session carries a user ID, not an email.
//
// The error is returned unwrapped so callers can errors.Is it against
// metrics.ErrUserNotFound — a session pointing at a deleted user has to be
// distinguishable from the database being unreachable.
func (s *userStore) LookupUserByID(ctx context.Context, id string) (domain.User, error) {
	return mongoWrap.FindUserByID(ctx, s.db.Collection(userCollectionName), id)
}

func (s *userStore) EditPassword(ctx context.Context, userId string, password string) error {
	return mongoWrap.EditUserPassword(ctx, s.db.Collection(userCollectionName), userId, password)
}

// CreateUser inserts an already-prepared user and returns the stored document.
//
// It takes a domain.User with PasswordHash already set rather than a signup
// request with a plaintext password: hashing is a security decision that belongs
// in the service layer next to the policy that validates the password, and
// keeping it there means plaintext never travels this far down.
func (s *userStore) CreateUser(ctx context.Context, user domain.User) (domain.User, error) {
	return mongoWrap.CreateUser(ctx, s.db.Collection(userCollectionName), user)
}
