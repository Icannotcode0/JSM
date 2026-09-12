package http

import (
	"net/http"

	"github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/http/handlers"
	"github.com/Icannotcode0/job-app-manager/backend/internal/http/middleware"
	ratelimit "github.com/Icannotcode0/job-app-manager/backend/internal/rate-limiter"
	"github.com/Icannotcode0/job-app-manager/backend/internal/service"
)

// NewRouter builds the full HTTP surface.
//
// The routing is deliberately "protected by default": authenticated routes go
// on their own mux which is wrapped once in SessionRequired, and the public
// surface is a short explicit list. Everything not on that list falls through
// to the protected mux.
//
// The alternative — one flat mux with SessionRequired wrapped around individual
// handlers — fails in the wrong direction. Forgetting a wrapper there produces
// no compile error and no startup error; the endpoint just quietly serves the
// user's job search to anyone who asks. Here, forgetting to make something
// public produces a 401 you notice on the first request.
func NewRouter(
	sm *authentication.SessionManager,
	services *service.Services,
	guard ratelimit.Guard,
) http.Handler {
	healthHandler := handlers.NewHealthHandler(services.Health)
	authHandler := handlers.NewAuth(services.Auth, sm, guard)
	userHandler := handlers.NewUserHandler(services.Users)
	applicationHandler := handlers.NewApplicationHandler(services.Applications)

	// ---- Authenticated ---------------------------------------------------

	protected := http.NewServeMux()
	protected.HandleFunc("GET /me", userHandler.Me)
	// Authenticated on purpose: changing a password requires proving you hold
	// the current one, which means having a session to prove it with. The
	// unauthenticated "forgot password" flow is a different endpoint and needs
	// a mail transport JSM does not have.
	protected.HandleFunc("POST /reset-password", authHandler.ResetPassword)
	protected.HandleFunc("GET /applications", applicationHandler.List)
	protected.HandleFunc("POST /applications", applicationHandler.Create)
	protected.HandleFunc("GET /applications/{id}", applicationHandler.Get)
	protected.HandleFunc("PATCH /applications/{id}", applicationHandler.Update)
	protected.HandleFunc("DELETE /applications/{id}", applicationHandler.Delete)

	// ---- Public ----------------------------------------------------------

	root := http.NewServeMux()
	root.HandleFunc("GET /health", healthHandler.HealthCheck)
	root.HandleFunc("POST /login", authHandler.Authenticate)
	root.HandleFunc("POST /signup", authHandler.SignUp)

	// Logout is public rather than session-gated on purpose. Behind
	// SessionRequired an already-expired session would get a 401, which is a
	// confusing answer to "sign me out" — the client is left holding a dead
	// cookie it was just refused permission to clear. API.md specifies 200 for
	// that case. It is still CSRF-protected like every other mutating route, so
	// a hostile page can't force a logout.
	root.HandleFunc("POST /logout", authHandler.Logout)

	// Catch-all: anything not matched above needs a session. ServeMux prefers
	// the most specific pattern, so the two public routes still win over "/".
	root.Handle("/", authentication.SessionRequired(sm)(protected))

	// CSRF wraps everything, not just the mutating routes: the middleware that
	// validates the token on POST/PATCH/DELETE is also what issues it on GET,
	// so GET /health is the bootstrap a cold client uses to obtain one.
	// Recover is outermost so it catches panics from the layers below it too.
	return chain(root,
		authentication.CSRFMiddleWare(sm),
		middleware.LoggingMiddleware(),
		middleware.RecoverMiddleware(),
	)
}

// chain applies middleware to h. The last argument ends up outermost, so the
// call reads in the order a request traverses it.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := range mws {
		h = mws[i](h)
	}
	return h
}
