// Package config loads runtime configuration from the environment (and an
// optional .env file sitting next to the binary or in the backend/ directory).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

type Config struct {
	// HTTP
	Port        string
	CORSOrigins []string

	// Database
	DatabaseURL string

	// Groww credentials. Either supply GROWW_ACCESS_TOKEN directly (expires
	// daily) or supply GROWW_API_KEY + GROWW_API_SECRET so the server can mint
	// and refresh access tokens itself via the checksum flow.
	GrowwAPIKey      string
	GrowwAPISecret   string
	GrowwAccessToken string
	GrowwBaseURL     string

	// MarketDataProviders is the ordered fallback chain, e.g. "groww,yahoo,mock".
	// The first source that answers wins; a failing one is skipped for a
	// cool-off window and retried later. Keep "mock" last so the platform stays
	// usable when nothing else is reachable — drop it to fail loudly instead.
	MarketDataProviders []string

	// InstrumentsURL is Groww's public instrument master (no auth needed).
	InstrumentsURL string
	InstrumentsDir string

	// Paper account
	StartingCash       decimal.Decimal
	DefaultAccountName string

	// How often the background matcher tries to fill resting LIMIT orders.
	MatchInterval time.Duration
}

func Load() (Config, error) {
	// Best effort: a missing .env is not an error.
	_ = godotenv.Load(".env", "backend/.env")

	cash, err := decimal.NewFromString(env("STARTING_CASH", "1000000"))
	if err != nil {
		return Config{}, fmt.Errorf("STARTING_CASH: %w", err)
	}

	interval, err := time.ParseDuration(env("MATCH_INTERVAL", "5s"))
	if err != nil {
		return Config{}, fmt.Errorf("MATCH_INTERVAL: %w", err)
	}

	cfg := Config{
		Port:                env("PORT", "8000"),
		CORSOrigins:         splitCSV(env("CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173")),
		DatabaseURL:         env("DATABASE_URL", "postgres://paper:paper@localhost:5432/paper_trading?sslmode=disable"),
		GrowwAPIKey:         os.Getenv("GROWW_API_KEY"),
		GrowwAPISecret:      os.Getenv("GROWW_API_SECRET"),
		GrowwAccessToken:    os.Getenv("GROWW_ACCESS_TOKEN"),
		GrowwBaseURL:        env("GROWW_BASE_URL", "https://api.groww.in/v1"),
		MarketDataProviders: splitCSV(strings.ToLower(env("MARKET_DATA_PROVIDERS", "groww,yahoo,mock"))),
		InstrumentsURL:      env("INSTRUMENTS_URL", "https://growwapi-assets.groww.in/instruments/instrument.csv"),
		InstrumentsDir:      env("INSTRUMENTS_CACHE_DIR", os.TempDir()),
		StartingCash:        cash,
		DefaultAccountName:  env("DEFAULT_ACCOUNT_NAME", "default"),
		MatchInterval:       interval,
	}

	if len(cfg.MarketDataProviders) == 0 {
		return Config{}, fmt.Errorf("MARKET_DATA_PROVIDERS must name at least one of: groww, yahoo, mock")
	}
	for _, p := range cfg.MarketDataProviders {
		switch p {
		case "groww", "yahoo", "mock":
		default:
			return Config{}, fmt.Errorf("unknown market data provider %q (want groww, yahoo or mock)", p)
		}
	}
	return cfg, nil
}

// HasGrowwCredentials reports whether we have enough to attempt live data.
func (c Config) HasGrowwCredentials() bool {
	return c.GrowwAccessToken != "" || (c.GrowwAPIKey != "" && c.GrowwAPISecret != "")
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// EnvBool is exported for the rare boolean switch; kept tiny on purpose.
func EnvBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return v
}
