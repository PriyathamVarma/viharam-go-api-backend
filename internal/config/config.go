package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env                string
	HTTPHost           string
	HTTPPort           string
	DatabaseURL        string
	MongoURI           string
	MongoDatabase      string
	JWTSecret          string
	JWTExpiresIn       time.Duration
	CORSAllowedOrigins []string
	RateLimitRPS       float64
	RateLimitBurst     int
	MaxBodyBytes       int
	LogLevel           slog.Level
	MigrateOnStartup   bool
	ShutdownTimeout    time.Duration
	UploadDir          string
	MaxUploadBytes     int64
	PublicBaseURL      string
}

func Load() (*Config, error) {
	c := &Config{
		Env:                getString("APP_ENV", "development"),
		HTTPHost:           getString("HTTP_HOST", "0.0.0.0"),
		HTTPPort:           getString("HTTP_PORT", "8080"),
		DatabaseURL:        firstNonEmpty(os.Getenv("SUPABASE_DB_URL"), os.Getenv("DATABASE_URL")),
		MongoURI:           strings.TrimSpace(os.Getenv("MONGODB_URI")),
		MongoDatabase:      getString("MONGODB_DATABASE", "viharam"),
		JWTSecret:          strings.TrimSpace(os.Getenv("JWT_SECRET")),
		JWTExpiresIn:       getDuration("JWT_EXPIRES_IN", 7*24*time.Hour),
		CORSAllowedOrigins: splitCSV(getString("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
		RateLimitRPS:       getFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst:     getInt("RATE_LIMIT_BURST", 40),
		MaxBodyBytes:       getInt("MAX_BODY_BYTES", 1<<20),
		LogLevel:           parseLevel(getString("LOG_LEVEL", "info")),
		MigrateOnStartup:   getBool("MIGRATE_ON_STARTUP", false),
		ShutdownTimeout:    getDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		UploadDir:          getString("UPLOAD_DIR", "./uploads"),
		MaxUploadBytes:     int64(getInt("MAX_UPLOAD_BYTES", 10<<20)),
		PublicBaseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
	}
	if c.DatabaseURL == "" && c.MongoURI == "" {
		return nil, errors.New("SUPABASE_DB_URL/DATABASE_URL or MONGODB_URI is required")
	}
	if len(c.JWTSecret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}
	if c.HTTPPort == "" {
		return nil, errors.New("HTTP_PORT must not be empty")
	}
	return c, nil
}

func (c *Config) Address() string { return c.HTTPHost + ":" + c.HTTPPort }

func getString(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
func getInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
func getFloat(key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
func getBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return v
}
func getDuration(key string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
func parseLevel(value string) slog.Level {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
