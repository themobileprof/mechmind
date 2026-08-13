package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	JWTSecret   string
	JWTTTL      time.Duration

	LLMEnabled bool
	LLMAPIKey  string
	LLMBaseURL string
	LLMModel   string
}

func Load() Config {
	_ = godotenv.Load()
	ttl := 24 * time.Hour
	if v := os.Getenv("JWT_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			ttl = d
		}
	}
	return Config{
		HTTPAddr:    envOr("HTTP_ADDR", ":8080"),
		DatabaseURL: envOr("DATABASE_URL", "postgres://autoservice:autoservice@127.0.0.1:5432/autoservice?sslmode=disable"),
		JWTSecret:   envOr("JWT_SECRET", ""),
		JWTTTL:      ttl,
		LLMEnabled:  envOr("LLM_ENABLED", "false") == "true",
		LLMAPIKey:   os.Getenv("LLM_API_KEY"),
		LLMBaseURL:  envOr("LLM_BASE_URL", "https://api.openai.com/v1"),
		LLMModel:    envOr("LLM_MODEL", "gpt-4o-mini"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
