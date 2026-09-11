package service

import (
	"context"

	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
)

type users struct {
	store *store.Store
}

func NewUsers(s *store.Store) *users {
	return &users{store: s}
}

// Me resolves the user behind the current session.
//
// The handler gets userID from the session context, never from the request
// body or a path parameter — otherwise /me would be an arbitrary user-lookup
// endpoint wearing a friendly name.
//
// domain.User tags PasswordHash `json:"-"`, so the hash cannot leak through the
// response even though it is loaded here.
func (u *users) Me(ctx context.Context, userID string) (domain.User, error) {
	// No translation step: the store already returns metrics.ErrUserNotFound,
	// which is the same value the handler checks against.
	return u.store.AuthenticateStore.LookupUserByID(ctx, userID)
}
