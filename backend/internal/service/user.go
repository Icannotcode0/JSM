package service

import (
	"context"
	"errors"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/mongoWrap"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
)

// ErrUserNotFound means the session named a user that no longer exists.
var ErrUserNotFound = errors.New("user not found")

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
	user, err := u.store.AuthenticateStore.LookupUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, mongoWrap.ErrUserNotFound) {
			return domain.User{}, ErrUserNotFound
		}
		return domain.User{}, err
	}
	return user, nil
}
