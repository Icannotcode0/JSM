package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/mongoWrap"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/redisWrap"
	"github.com/Icannotcode0/job-app-manager/backend/internal/config"
	jsmhttp "github.com/Icannotcode0/job-app-manager/backend/internal/http"
	"github.com/Icannotcode0/job-app-manager/backend/internal/service"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
)

const (
	startupTimeout  = 15 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("jsm: %v", err)
	}
}

// run holds the real body so every deferred Close actually executes — a
// log.Fatal inside main would skip them and leak the Mongo and Redis
// connections on any startup failure.
func run() error {
	cfg := config.Load()

	// Interrupt-aware from the very first connection attempt, so Ctrl-C during
	// a hung startup dial exits instead of waiting out the timeout.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	// ---- Data stores -----------------------------------------------------

	mongoClient, err := mongoWrap.NewClient(
		startupCtx,
		cfg.Mongo.URI,
		cfg.Mongo.Database,
		cfg.Mongo.UsersCollection,
		cfg.Mongo.AppsCollection,
	)
	if err != nil {
		return err
	}
	defer func() {
		if err := mongoClient.Close(context.Background()); err != nil {
			log.Printf("mongo disconnect: %v", err)
		}
	}()

	if err := mongoClient.EnsureIndexes(startupCtx); err != nil {
		return err
	}

	redisClient, err := redisWrap.NewClient(startupCtx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Printf("redis close: %v", err)
		}
	}()

	// ---- Wiring ----------------------------------------------------------

	sessionManager, err := redisWrap.NewSessionManager(redisClient, cfg.Cookie)
	if err != nil {
		return err
	}
	store := store.NewStore(mongoClient.DB)
	services := service.NewServices(store, sessionManager, redisClient)

	// Routing, and the public/authenticated split it encodes, lives in
	// internal/http so main.go stays wiring only.
	srv := &http.Server{
		Addr:              localOnly(cfg.HTTPPort),
		Handler:           jsmhttp.NewRouter(sessionManager, services),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ---- Serve -----------------------------------------------------------

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("jsm listening on %s (env=%s)", srv.Addr, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		log.Print("jsm: shutting down")
	}

	// Fresh context: ctx is already cancelled by the signal, and passing a
	// cancelled context to Shutdown would kill in-flight requests immediately
	// instead of letting them drain.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()

	return srv.Shutdown(shutdownCtx)
}

// chain applies middleware to h. The last argument ends up outermost, so it
// reads in the order requests traverse it: chain(mux, csrf, recover) runs
// recover first, then csrf, then the mux — which is what you want, since a
// panic in the CSRF layer still needs catching.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {

	for i := range mws {
		h = mws[i](h)
	}
	return h
}

// localOnly forces the listen address onto the loopback interface.
//
// A bare ":8080" (the config default) binds every interface, which would put a
// single-user tool holding your job search on the local network — DESIGN_GUIDE
// Part 6 and API.md both specify 127.0.0.1 only. An address that already names
// a host is left alone, so an explicit override still works.
func localOnly(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}
