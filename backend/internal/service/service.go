package service

import (
	"context"
	"net/http"

	"github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/config"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
	"github.com/redis/go-redis/v9"
)

// The interfaces below are exported so the handler package can depend on a
// single capability rather than the whole Services struct, and so tests can
// declare fakes that satisfy them. Unexported interfaces behind exported
// fields can be called but never named, which makes them impossible to mock
// from outside this package.

// HealthChecker reports whether the backing stores are reachable.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// Authenticator establishes and ends authenticated sessions. Both methods
// return cookies rather than writing them, so the session policy stays in one
// place and the handler's only job is to set what it is given.
type Authenticator interface {
	Authenticate(ctx context.Context, email string, password string) ([]*http.Cookie, error)
	ChangePassword(ctx context.Context, userId string, sessionID string, req domain.ChangePasswordRequest) ([]*http.Cookie, error)
	CreateUser(ctx context.Context, req domain.SignUpRequest) (domain.User, error)
	Logout(ctx context.Context, sessionID string) ([]*http.Cookie, error)
}

// UserReader resolves the user behind a session.
type UserReader interface {
	Me(ctx context.Context, userID string) (domain.User, error)
}

// ApplicationService is the full CRUD + search surface behind /applications.
// userID is a parameter on every method, not an ambient value, so ownership is
// always explicit at the call site.
type ApplicationService interface {
	Create(ctx context.Context, userID string, req domain.CreateApplicationRequest) (domain.Application, error)
	Get(ctx context.Context, userID, id string) (domain.Application, error)
	List(ctx context.Context, userID string, query domain.ListApplicationsQuery) (domain.ApplicationPage, error)
	Update(ctx context.Context, userID, id string, req domain.UpdateApplicationRequest) (domain.Application, error)
	Delete(ctx context.Context, userID, id string) error
}

// Services is the aggregate the HTTP layer is wired against.
type Services struct {
	Health       HealthChecker
	Auth         Authenticator
	Users        UserReader
	Applications ApplicationService
}

func NewServices(
	store *store.Store,
	sm *authentication.SessionManager,
	rds *redis.Client,
	mail config.MailConfig,
) *Services {
	return &Services{
		Health:       NewHealth(store, rds),
		Auth:         NewAuth(store, sm, mail),
		Users:        NewUsers(store),
		Applications: NewApplications(store),
	}
}
