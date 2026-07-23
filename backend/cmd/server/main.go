// Command server runs the Groww paper-trading API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/gangrajat/groww-paper-trading/backend/internal/analytics"
	"github.com/gangrajat/groww-paper-trading/backend/internal/auth"
	"github.com/gangrajat/groww-paper-trading/backend/internal/config"
	"github.com/gangrajat/groww-paper-trading/backend/internal/groww"
	"github.com/gangrajat/groww-paper-trading/backend/internal/httpapi"
	"github.com/gangrajat/groww-paper-trading/backend/internal/instruments"
	"github.com/gangrajat/groww-paper-trading/backend/internal/intraday"
	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/paper"
	"github.com/gangrajat/groww-paper-trading/backend/internal/signals"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
	"github.com/gangrajat/groww-paper-trading/backend/internal/yahoo"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Emit money as JSON numbers, not quoted strings, so the TypeScript client
	// gets `number` everywhere. Precision is still exact on the wire.
	decimal.MarshalJSONWithoutQuotes = true

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	market := buildMarketData(cfg, log)
	probeMarketData(ctx, market, log)

	// The instrument master is only needed for symbol search and display names,
	// so a failure here degrades search rather than blocking trading.
	universe := instruments.New(instruments.Options{
		URL:      cfg.InstrumentsURL,
		CacheDir: cfg.InstrumentsDir,
		Segments: []string{"CASH"},
	}, log)
	if err := universe.Load(ctx); err != nil {
		log.Warn("instrument universe unavailable; symbol search disabled", "err", err)
	}

	engine := paper.New(st, market, log)
	scanner := analytics.New(market, universe, log)
	orb := intraday.NewScanner(market, universe, log)
	board := signals.New(scanner, orb, st, market)
	authSvc := auth.New(st)

	// Resting LIMIT orders are matched in the background for every account,
	// so exits fire even while nobody has the dashboard open.
	go engine.RunMatcher(ctx, cfg.MatchInterval)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpapi.NewServer(engine, st, market, universe, scanner, orb, board, authSvc, log).Routes(cfg.CORSOrigins),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// buildMarketData assembles the provider chain named by MARKET_DATA_PROVIDERS.
//
// Default order is groww → yahoo → mock: the real broker feed first, a public
// feed that needs no subscription second, and the simulator last so the
// platform is never dead in the water.
func buildMarketData(cfg config.Config, log *slog.Logger) marketdata.Provider {
	var providers []marketdata.Provider

	for _, name := range cfg.MarketDataProviders {
		switch name {
		case "groww":
			if !cfg.HasGrowwCredentials() {
				log.Warn("skipping groww market data: no API key/secret or access token configured")
				continue
			}
			tokens := groww.NewTokenSource(cfg.GrowwBaseURL, cfg.GrowwAPIKey, cfg.GrowwAPISecret,
				cfg.GrowwAccessToken, &http.Client{Timeout: 15 * time.Second})
			providers = append(providers, groww.NewClient(cfg.GrowwBaseURL, tokens))
		case "yahoo":
			providers = append(providers, yahoo.NewClient())
		case "mock":
			providers = append(providers, marketdata.NewMock())
		}
	}

	if len(providers) == 0 {
		log.Warn("no market data provider could be configured; falling back to the simulator")
		providers = append(providers, marketdata.NewMock())
	}
	return marketdata.NewChain(log, providers...)
}

// probeMarketData asks for one price at boot so /health reports the source that
// will actually serve requests, rather than an optimistic guess until the first
// real lookup fails.
func probeMarketData(ctx context.Context, market marketdata.Provider, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if _, err := market.LTP(ctx, "NSE", "CASH", "RELIANCE"); err != nil {
		log.Warn("market data probe failed", "err", err)
		return
	}
	if chain, ok := market.(*marketdata.Chain); ok {
		log.Info("market data ready", "source", chain.Active())
	}
}
