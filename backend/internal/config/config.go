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

type Config struct {
	Env      string
	HTTPPort string
	Mongo    MongoConfig
	Redis    RedisConfig
	Cookie   CookieConfig
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
