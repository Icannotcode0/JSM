package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type MongoConfig struct {
	URI             string
	Database        string
	UsersCollection string
	AppsCollection  string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type CookieConfig struct {
	SessionCookieName string
	CSRFCookieName    string
	Domain            string
	Secure            bool
	TTL               time.Duration
	Secret            string
}

// MailConfig controls whether JSM sends mail at all, and how.
type MailConfig struct {
	// RequireEmailVerification gates the signup verification round-trip.
	//
	// False — the local default — marks new accounts verified immediately and
	// sends nothing, so a local install keeps the promise that nothing leaves
	// the machine and works with no mail provider. A hosted deployment sets it
	// true, and then an address has to be proved before the account is usable.
	//
	// The same code path runs either way; only this flag differs.
	RequireEmailVerification bool

	// FromAddress is the envelope sender for anything JSM does send.
	FromAddress string
}

// RateLimitConfig describes what the deployment sits behind.
//
// TrustedProxies is empty by default, which means the TCP peer is used. That is
// the safe default: forgetting to declare a proxy keys every user on the load
// balancer and locks out the world within seconds — loud and immediate — while
// trusting too broadly is a silent bypass found after the fact.
type RateLimitConfig struct {
	Enabled        bool
	TrustedProxies string // comma-separated CIDRs or addresses
	ClientIPHeader string // e.g. CF-Connecting-IP, Fly-Client-IP
}

type Config struct {
	Env       string
	HTTPPort  string
	Mongo     MongoConfig
	Redis     RedisConfig
	Cookie    CookieConfig
	Mail      MailConfig
	RateLimit RateLimitConfig
}

// Load reads configuration from process environment variables, first
// loading any KEY=VALUE pairs from a ".env" file in the working directory
// (if present) without overriding variables already set in the environment.
// Missing values fall back to sane local-dev defaults.
func Load() Config {
	loadDotEnvIfPresent(".env")

	ttlHours, err := strconv.Atoi(getEnv("SESSION_TTL_HOURS", "168"))
	if err != nil {
		ttlHours = 168
	}
	redisDB, err := strconv.Atoi(getEnv("REDIS_DB", "0"))
	if err != nil {
		redisDB = 0
	}
	cookieSecure, err := strconv.ParseBool(getEnv("COOKIE_SECURE", "false"))
	if err != nil {
		cookieSecure = false
	}
	// Defaults to false: an unparseable value must not silently switch on a
	// requirement the deployment has no mail transport to satisfy.
	requireVerification, err := strconv.ParseBool(getEnv("REQUIRE_EMAIL_VERIFICATION", "false"))
	if err != nil {
		requireVerification = false
	}
	// Defaults to on. An unparseable value must not silently disable a control
	// whose absence is invisible until someone exploits it.
	rateLimitEnabled, err := strconv.ParseBool(getEnv("RATE_LIMIT_ENABLED", "true"))
	if err != nil {
		rateLimitEnabled = true
	}

	return Config{
		Env:      getEnv("APP_ENV", "dev"),
		HTTPPort: getEnv("HTTP_PORT", ":8080"),
		Mongo: MongoConfig{
			URI:             getEnv("MONGO_URI", "mongodb://127.0.0.1:27017"),
			Database:        getEnv("MONGO_DB", "jobtracker"),
			UsersCollection: getEnv("MONGO_USERS_COLLECTION", "users"),
			AppsCollection:  getEnv("MONGO_APPLICATIONS_COLLECTION", "applications"),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "127.0.0.1:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       redisDB,
		},
		Cookie: CookieConfig{
			SessionCookieName: getEnv("SESSION_COOKIE_NAME", "jsm_session"),
			CSRFCookieName:    getEnv("CSRF_COOKIE_NAME", "jsm_csrf"),
			Domain:            getEnv("COOKIE_DOMAIN", ""),
			Secure:            cookieSecure,
			TTL:               time.Duration(ttlHours) * time.Hour,
			Secret:            getEnv("SESSION_SECRET", ""),
		},
		RateLimit: RateLimitConfig{
			Enabled:        rateLimitEnabled,
			TrustedProxies: getEnv("TRUSTED_PROXIES", ""),
			ClientIPHeader: getEnv("CLIENT_IP_HEADER", ""),
		},
		Mail: MailConfig{
			RequireEmailVerification: requireVerification,
			FromAddress:              getEnv("MAIL_FROM", "jsm@localhost"),
		},
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// loadDotEnvIfPresent does a minimal KEY=VALUE parse of path, setting
// process env vars for keys not already set. Missing file is not an error —
// env vars can always be supplied another way (shell export, Docker, etc).
func loadDotEnvIfPresent(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() {
		file.Close()
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if _, alreadySet := os.LookupEnv(key); !alreadySet {
			os.Setenv(key, value)
		}
	}
}
