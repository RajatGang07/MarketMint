// Package httpapi exposes the paper-trading engine over REST.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/gangrajat/groww-paper-trading/backend/internal/analytics"
	"github.com/gangrajat/groww-paper-trading/backend/internal/auth"
	"github.com/gangrajat/groww-paper-trading/backend/internal/instruments"
	"github.com/gangrajat/groww-paper-trading/backend/internal/intraday"
	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/paper"
	"github.com/gangrajat/groww-paper-trading/backend/internal/signals"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
	"github.com/gangrajat/groww-paper-trading/backend/internal/web"
)

// Server holds everything the handlers need. The account is resolved once at
// startup — v1 is a single-account platform, so this is the seam where
// per-user auth would slot in.
type Server struct {
	engine    *paper.Engine
	store     *store.Store
	market    marketdata.Provider
	universe  *instruments.Store
	analytics *analytics.Engine
	intraday  *intraday.Scanner
	signals   *signals.Composer
	auth      *auth.Service
	log       *slog.Logger
}

// ctxKey scopes context values to this package.
type ctxKey int

const accountKey ctxKey = iota

// accountFrom returns the authenticated account injected by requireAuth.
func accountFrom(r *http.Request) store.Account {
	acct, _ := r.Context().Value(accountKey).(store.Account)
	return acct
}

func NewServer(
	engine *paper.Engine,
	st *store.Store,
	market marketdata.Provider,
	universe *instruments.Store,
	analyticsEngine *analytics.Engine,
	intradayScanner *intraday.Scanner,
	signalsBoard *signals.Composer,
	authService *auth.Service,
	log *slog.Logger,
) *Server {
	return &Server{
		engine:    engine,
		store:     st,
		market:    market,
		universe:  universe,
		analytics: analyticsEngine,
		intraday:  intradayScanner,
		signals:   signalsBoard,
		auth:      authService,
		log:       log,
	}
}

// requireAuth resolves the bearer token to an account or answers 401.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		acct, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			var cred auth.CredentialError
			if errors.As(err, &cred) {
				writeErr(w, http.StatusUnauthorized, cred.Reason)
				return
			}
			s.log.Error("auth lookup failed", "err", err)
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey, acct)))
	})
}

// Routes builds the router, including CORS for the Vite dev server.
func (s *Server) Routes(corsOrigins []string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	// Everything gets a tight timeout except the universe scan, whose first
	// uncached run fans out a couple hundred candle fetches.
	quick := middleware.Timeout(30 * time.Second)

	r.With(quick).Get("/health", s.handleHealth)

	r.Route("/auth", func(r chi.Router) {
		r.Use(quick)
		r.Post("/signup", s.handleSignup)
		r.Post("/login", s.handleLogin)
		r.Post("/logout", s.handleLogout)
		r.With(s.requireAuth).Get("/me", s.handleMe)
	})

	r.Route("/market", func(r chi.Router) {
		r.Use(quick)
		r.Get("/ltp", s.handleLTP)
		r.Get("/quote", s.handleQuote)
		r.Get("/quotes", s.handleQuotes)
		r.Get("/candles", s.handleCandles)
	})

	r.With(quick).Get("/instruments/search", s.handleSearchInstruments)

	slow := middleware.Timeout(3 * time.Minute)
	r.With(slow, s.requireAuth).Get("/analytics/recommendations", s.handleRecommendations)
	r.With(slow, s.requireAuth).Get("/analytics/intraday", s.handleIntraday)
	r.With(slow, s.requireAuth).Get("/analytics/signals", s.handleSignals)

	r.Route("/orders", func(r chi.Router) {
		r.Use(quick, s.requireAuth)
		r.Get("/", s.handleListOrders)
		r.Post("/", s.handlePlaceOrder)
		r.Post("/{orderRef}/cancel", s.handleCancelOrder)
	})

	r.With(quick, s.requireAuth).Get("/trades", s.handleListTrades)

	r.Route("/portfolio", func(r chi.Router) {
		r.Use(quick, s.requireAuth)
		r.Get("/", s.handlePortfolio)
		r.Post("/reset", s.handleReset)
		r.Post("/equity", s.handleSetEquity)
	})

	// Everything that isn't an API route serves the embedded dashboard:
	// real files (assets, favicon) directly, anything else the SPA shell.
	// This is what lets one free-tier service host the whole app.
	staticServer := http.FileServer(http.FS(web.Dist()))
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path != "" {
			if f, err := web.Dist().Open(path); err == nil {
				f.Close()
				staticServer.ServeHTTP(w, req)
				return
			}
		}
		req.URL.Path = "/" // SPA fallback
		staticServer.ServeHTTP(w, req)
	})

	return r
}

// ---------------------------------------------------------------------------
// Health / meta
// ---------------------------------------------------------------------------

// marketDataMode is "live" whenever a real feed is serving prices, and "mock"
// when we've fallen through to the simulator. The UI leads with this, because
// trading against simulated prices without realising it would be the single
// most misleading thing this app could do.
func (s *Server) marketDataMode() string {
	if chain, ok := s.market.(*marketdata.Chain); ok {
		if chain.Active() == "simulator" {
			return "mock"
		}
		return "live"
	}
	if s.market.Name() == "simulator" {
		return "mock"
	}
	return "live"
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"status":           "ok",
		"market_data_mode": s.marketDataMode(),
	}

	if chain, ok := s.market.(*marketdata.Chain); ok {
		statuses := chain.Status()
		body["market_data_source"] = chain.Active()
		body["market_data_providers"] = statuses

		// Surface why the preferred sources aren't being used, so the banner
		// in the UI can say something actionable.
		var notes []string
		for _, p := range statuses {
			if !p.Healthy && p.Reason != "" {
				notes = append(notes, p.Name+": "+p.Reason)
			}
		}
		if len(notes) > 0 {
			body["market_data_note"] = strings.Join(notes, " · ")
		}
	}

	body["instruments_loaded"] = s.universe.Count()

	if err := s.store.Pool.Ping(r.Context()); err != nil {
		body["status"] = "degraded"
		body["database"] = err.Error()
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// ---------------------------------------------------------------------------
// Market data
// ---------------------------------------------------------------------------

func (s *Server) handleLTP(w http.ResponseWriter, r *http.Request) {
	symbols := splitSymbols(r.URL.Query().Get("symbols"))
	if len(symbols) == 0 {
		writeErr(w, http.StatusBadRequest, "symbols is required, e.g. ?symbols=RELIANCE,TCS")
		return
	}
	exchange := queryOr(r, "exchange", "NSE")
	segment := queryOr(r, "segment", "CASH")

	out := make([]ltpDTO, 0, len(symbols))
	for _, sym := range symbols {
		price, err := s.market.LTP(r.Context(), exchange, segment, sym)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "price lookup failed for "+sym+": "+err.Error())
			return
		}
		out = append(out, ltpDTO{Symbol: sym, Exchange: exchange, LTP: price.Round(2)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleQuote(w http.ResponseWriter, r *http.Request) {
	symbol := marketdata.Normalise(r.URL.Query().Get("symbol"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "symbol is required")
		return
	}
	exchange := queryOr(r, "exchange", "NSE")

	quote, err := s.market.Quote(r.Context(), exchange, queryOr(r, "segment", "CASH"), symbol)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "quote lookup failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.toQuoteDTO(quote))
}

// handleQuotes powers the watchlist: one round trip for every symbol on it,
// each carrying the day change the UI colours on.
//
// A symbol that can't be priced is returned with ok=false rather than failing
// the whole request — one delisted ticker shouldn't blank the watchlist.
func (s *Server) handleQuotes(w http.ResponseWriter, r *http.Request) {
	symbols := splitSymbols(r.URL.Query().Get("symbols"))
	if len(symbols) == 0 {
		writeErr(w, http.StatusBadRequest, "symbols is required, e.g. ?symbols=RELIANCE,TCS")
		return
	}
	if len(symbols) > 50 {
		writeErr(w, http.StatusBadRequest, "at most 50 symbols per request")
		return
	}
	exchange := queryOr(r, "exchange", "NSE")
	segment := queryOr(r, "segment", "CASH")

	out := make([]quoteDTO, 0, len(symbols))
	for _, sym := range symbols {
		quote, err := s.market.Quote(r.Context(), exchange, segment, sym)
		if err != nil {
			out = append(out, quoteDTO{Symbol: sym, Exchange: exchange, OK: false, Error: err.Error()})
			continue
		}
		out = append(out, s.toQuoteDTO(quote))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCandles serves chart history. `range` is a friendly preset (1d, 5d,
// 1mo, 3mo, 1y) and the bar interval is derived from it unless overridden.
func (s *Server) handleCandles(w http.ResponseWriter, r *http.Request) {
	symbol := marketdata.Normalise(r.URL.Query().Get("symbol"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "symbol is required")
		return
	}

	preset := strings.ToLower(queryOr(r, "range", "1D"))
	span, interval, ok := rangePreset(preset)
	if !ok {
		writeErr(w, http.StatusBadRequest, "range must be one of 1d, 5d, 1mo, 3mo, 1y")
		return
	}
	if override := r.URL.Query().Get("interval_minutes"); override != "" {
		n, err := strconv.Atoi(override)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "interval_minutes must be a positive integer")
			return
		}
		interval = n
	}

	end := time.Now()
	candles, err := s.market.Candles(r.Context(), marketdata.CandleRequest{
		Exchange:        queryOr(r, "exchange", "NSE"),
		Segment:         queryOr(r, "segment", "CASH"),
		Symbol:          symbol,
		IntervalMinutes: interval,
		Start:           end.Add(-span),
		End:             end,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "candle lookup failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"symbol":           symbol,
		"range":            preset,
		"interval_minutes": interval,
		"candles":          toCandleDTOs(candles),
	})
}

// rangePreset maps a UI range button onto a lookback window and bar size.
func rangePreset(preset string) (span time.Duration, intervalMinutes int, ok bool) {
	switch preset {
	case "1d":
		return 24 * time.Hour, 5, true
	case "5d":
		return 5 * 24 * time.Hour, 15, true
	case "1mo":
		return 30 * 24 * time.Hour, 60, true
	case "3mo":
		return 90 * 24 * time.Hour, 1440, true
	case "1y":
		return 365 * 24 * time.Hour, 1440, true
	default:
		return 0, 0, false
	}
}

func (s *Server) handleSearchInstruments(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, []instruments.Instrument{})
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	results := s.universe.Search(query, queryOr(r, "exchange", "NSE"), limit)
	if results == nil {
		results = []instruments.Instrument{}
	}
	writeJSON(w, http.StatusOK, results)
}

// handleRecommendations runs the momentum screen over the F&O universe and
// sizes the top ideas against the account's free cash.
//
// The first uncached call fans out a couple hundred candle fetches and takes
// 10-30s; after that it serves from a 15-minute cache.
func (s *Server) handleRecommendations(w http.ResponseWriter, r *http.Request) {
	account := accountFrom(r)
	bands := analytics.DefaultRiskBands
	readBand := func(key string, into *float64) bool {
		v := r.URL.Query().Get(key)
		if v == "" {
			return true
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			writeErr(w, http.StatusBadRequest, key+" must be a positive number")
			return false
		}
		*into = f
		return true
	}
	if !readBand("loss_min", &bands.LossMin) || !readBand("loss_max", &bands.LossMax) ||
		!readBand("profit_min", &bands.ProfitMin) || !readBand("profit_max", &bands.ProfitMax) {
		return
	}
	if bands.LossMin > bands.LossMax || bands.ProfitMin > bands.ProfitMax {
		writeErr(w, http.StatusBadRequest, "band minimums must not exceed maximums")
		return
	}

	topN := 5
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 15 {
			topN = n
		}
	}

	cash, _ := account.Cash.Float64()

	res, err := s.analytics.Recommend(r.Context(), bands, cash, topN)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "scan failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleIntraday runs the ORB scanner. risk (default 5000) is the rupee loss
// accepted per trade; sizing scales from it.
func (s *Server) handleIntraday(w http.ResponseWriter, r *http.Request) {
	risk := 5000.0
	if v := r.URL.Query().Get("risk"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			writeErr(w, http.StatusBadRequest, "risk must be a positive number")
			return
		}
		risk = f
	}
	topN := 10
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 30 {
			topN = n
		}
	}

	account := accountFrom(r)
	cash, _ := account.Cash.Float64()

	res, err := s.intraday.Scan(r.Context(), risk, cash, topN)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "intraday scan failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleSignals serves the one-table verdict board: BUY / SELL / WATCH / HOLD
// per stock, composed from the positional ranking, today's intraday state and
// the account's holdings.
func (s *Server) handleSignals(w http.ResponseWriter, r *http.Request) {
	account := accountFrom(r)
	cash, _ := account.Cash.Float64()

	board, err := s.signals.Compose(r.Context(), account.ID, analytics.DefaultRiskBands, cash)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "signals failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, board)
}

// ---------------------------------------------------------------------------
// Trading
// ---------------------------------------------------------------------------

func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req paper.OrderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	order, err := s.engine.PlaceOrder(r.Context(), accountFrom(r), req)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderDTO(order))
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.store.ListOrders(r.Context(), accountFrom(r).ID, 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderDTOs(orders))
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.engine.CancelOrder(r.Context(), accountFrom(r).ID, chi.URLParam(r, "orderRef"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrderDTO(order))
}

func (s *Server) handleListTrades(w http.ResponseWriter, r *http.Request) {
	trades, err := s.store.ListTrades(r.Context(), accountFrom(r).ID, 200)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTradeDTOs(trades))
}

func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	view, err := s.engine.Portfolio(r.Context(), accountFrom(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if err := s.engine.Reset(r.Context(), accountFrom(r).ID); err != nil {
		s.fail(w, err)
		return
	}
	s.refreshedPortfolio(w, r)
}

// ---------------------------------------------------------------------------
// Auth & account
// ---------------------------------------------------------------------------

type credentialsBody struct {
	Username     string           `json:"username"`
	Password     string           `json:"password"`
	StartingCash *decimal.Decimal `json:"starting_cash"`
}

type sessionResponse struct {
	Token    string          `json:"token"`
	Username string          `json:"username"`
	Equity   decimal.Decimal `json:"starting_cash"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var body credentialsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cash := decimal.Zero
	if body.StartingCash != nil {
		cash = *body.StartingCash
	}
	acct, token, err := s.auth.Signup(r.Context(), body.Username, body.Password, cash)
	if err != nil {
		s.failAuth(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{Token: token, Username: acct.Name, Equity: acct.StartingCash})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body credentialsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	acct, token, err := s.auth.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		s.failAuth(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{Token: token, Username: acct.Name, Equity: acct.StartingCash})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token != "" {
		_ = s.auth.Logout(r.Context(), token)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acct := accountFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":      acct.Name,
		"starting_cash": acct.StartingCash,
		"cash":          acct.Cash,
	})
}

// handleSetEquity re-bases the account at a user-chosen starting equity.
// Destructive by design: positions and history describe the old bankroll.
func (s *Server) handleSetEquity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StartingCash decimal.Decimal `json:"starting_cash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.StartingCash.LessThan(auth.MinStartingCash) || body.StartingCash.GreaterThan(auth.MaxStartingCash) {
		writeErr(w, http.StatusBadRequest, "starting equity must be between ₹"+auth.MinStartingCash.String()+" and ₹"+auth.MaxStartingCash.String())
		return
	}
	acct := accountFrom(r)
	err := s.store.InTx(r.Context(), func(tx pgx.Tx) error {
		if _, err := store.LockAccount(r.Context(), tx, acct.ID); err != nil {
			return err
		}
		return store.SetStartingCash(r.Context(), tx, acct.ID, body.StartingCash)
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.refreshedPortfolio(w, r)
}

// refreshedPortfolio re-reads the account and answers with the fresh view.
func (s *Server) refreshedPortfolio(w http.ResponseWriter, r *http.Request) {
	acct, err := s.store.GetAccount(r.Context(), accountFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	view, err := s.engine.Portfolio(r.Context(), acct)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// failAuth maps credential errors to 400s without leaking internals.
func (s *Server) failAuth(w http.ResponseWriter, err error) {
	var cred auth.CredentialError
	if errors.As(err, &cred) {
		writeErr(w, http.StatusBadRequest, cred.Reason)
		return
	}
	s.log.Error("auth failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// toQuoteDTO enriches a raw quote with day-change and the instrument's display
// name, which the watchlist and the chart header both want.
func (s *Server) toQuoteDTO(q marketdata.Quote) quoteDTO {
	dto := quoteDTO{
		Symbol:    q.Symbol,
		Exchange:  q.Exchange,
		LastPrice: q.LastPrice.Round(2),
		Open:      q.Open.Round(2),
		High:      q.High.Round(2),
		Low:       q.Low.Round(2),
		Close:     q.Close.Round(2),
		Volume:    q.Volume.Round(2),
		Change:    q.Change().Round(2),
		ChangePct: q.ChangePct().Round(2),
		OK:        true,
	}
	if inst, found := s.universe.Lookup(q.Exchange, q.Symbol); found {
		dto.Name = inst.Name
	}
	return dto
}

// fail maps engine errors onto status codes: business rejections are 400,
// missing rows are 404, everything else is a logged 500.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var rej paper.RejectError
	switch {
	case errors.As(err, &rej):
		writeErr(w, http.StatusBadRequest, rej.Reason)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	default:
		s.log.Error("request failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeErr uses the key "detail" so the frontend's single error path works for
// every endpoint.
func writeErr(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func queryOr(r *http.Request, key, fallback string) string {
	if v := strings.TrimSpace(r.URL.Query().Get(key)); v != "" {
		return strings.ToUpper(v)
	}
	return fallback
}

func splitSymbols(raw string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		p := marketdata.Normalise(part)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
