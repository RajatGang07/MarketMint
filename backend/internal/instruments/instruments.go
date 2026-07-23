// Package instruments holds the tradable universe: Groww publishes the full
// instrument master as a public CSV (no auth, no subscription), which gives the
// dashboard real symbol search over ~4,000 NSE cash instruments instead of a
// hardcoded list.
package instruments

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultURL is Groww's published instrument master.
const DefaultURL = "https://growwapi-assets.groww.in/instruments/instrument.csv"

// Instrument is one tradable line item, trimmed to what the UI needs.
type Instrument struct {
	TradingSymbol string `json:"trading_symbol"`
	Name          string `json:"name"`
	Exchange      string `json:"exchange"`
	Segment       string `json:"segment"`
	Series        string `json:"series"`
	ISIN          string `json:"isin"`
	LotSize       int    `json:"lot_size"`
	TickSize      string `json:"tick_size"`
	IsIntraday    bool   `json:"is_intraday"`
}

// Store is an in-memory, searchable index of the universe.
//
// The CSV is ~21 MB and mostly F&O contracts, so only the segments we actually
// trade are retained — that keeps the resident set small and search fast enough
// to scan linearly without an index structure.
type Store struct {
	url       string
	cachePath string
	segments  map[string]bool
	log       *slog.Logger

	mu          sync.RWMutex
	all         []Instrument
	bySym       map[string]Instrument
	derivatives map[string]bool
	loaded      time.Time
	loadErr     error
}

// Options configure the store. Zero values are sensible.
type Options struct {
	URL string
	// CacheDir persists the downloaded CSV so restarts don't re-fetch 21 MB.
	CacheDir string
	// Segments to retain, e.g. {"CASH"}. Empty keeps everything.
	Segments []string
}

func New(opts Options, log *slog.Logger) *Store {
	if opts.URL == "" {
		opts.URL = DefaultURL
	}
	if opts.CacheDir == "" {
		opts.CacheDir = os.TempDir()
	}

	segments := make(map[string]bool, len(opts.Segments))
	for _, s := range opts.Segments {
		segments[strings.ToUpper(s)] = true
	}

	return &Store{
		url:       opts.URL,
		cachePath: filepath.Join(opts.CacheDir, "groww-instruments.csv"),
		segments:  segments,
		log:       log,
	}
}

// Ready reports whether the universe is loaded.
func (s *Store) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.all) > 0
}

// Count is how many instruments are indexed.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.all)
}

// Err returns the last load failure, if any.
func (s *Store) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadErr
}

// Load fills the index, preferring a same-day local cache over the network.
// Failure is not fatal to the platform: search degrades, trading does not.
func (s *Store) Load(ctx context.Context) error {
	if data, err := s.readCache(); err == nil {
		if err := s.parse(strings.NewReader(string(data))); err == nil {
			s.log.Info("instrument universe loaded from cache", "count", s.Count(), "path", s.cachePath)
			return nil
		}
	}

	body, err := s.download(ctx)
	if err != nil {
		s.setErr(err)
		return err
	}
	if err := s.parse(strings.NewReader(string(body))); err != nil {
		s.setErr(err)
		return err
	}
	if err := os.WriteFile(s.cachePath, body, 0o600); err != nil {
		s.log.Warn("could not cache instrument master", "err", err)
	}
	s.log.Info("instrument universe downloaded", "count", s.Count())
	return nil
}

// readCache returns the cached CSV if it was written today. The master changes
// daily (new F&O contracts), so anything older is refetched.
func (s *Store) readCache() ([]byte, error) {
	info, err := os.Stat(s.cachePath)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > 12*time.Hour {
		return nil, errors.New("cache stale")
	}
	return os.ReadFile(s.cachePath)
}

func (s *Store) download(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("instruments: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instruments: download returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func (s *Store) parse(r io.Reader) error {
	reader := csv.NewReader(r)
	reader.ReuseRecord = true
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("instruments: read header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[strings.TrimSpace(name)] = i
	}

	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	var all []Instrument
	bySym := make(map[string]Instrument)
	// Symbols with listed derivatives. There is no liquidity column in the
	// master, but "has an F&O contract" is a good proxy for it — and it is what
	// makes a search for "REL" return RELIANCE rather than RELAXO.
	hasDerivatives := make(map[string]bool)

	for {
		rec, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue // one malformed line shouldn't sink the whole universe
		}

		segment := strings.ToUpper(get(rec, "segment"))
		if segment == "FNO" {
			if u := strings.ToUpper(get(rec, "underlying_symbol")); u != "" {
				hasDerivatives[u] = true
			}
		}
		if len(s.segments) > 0 && !s.segments[segment] {
			continue
		}

		inst := Instrument{
			TradingSymbol: strings.ToUpper(get(rec, "trading_symbol")),
			Name:          get(rec, "name"),
			Exchange:      strings.ToUpper(get(rec, "exchange")),
			Segment:       segment,
			Series:        strings.ToUpper(get(rec, "series")),
			ISIN:          get(rec, "isin"),
			LotSize:       atoi(get(rec, "lot_size")),
			TickSize:      get(rec, "tick_size"),
			IsIntraday:    get(rec, "is_intraday") == "1",
		}
		if inst.TradingSymbol == "" {
			continue
		}

		// The master lists some instruments more than once (multiple series on
		// the same ticker); keep the first and don't show the user duplicates.
		key := inst.Exchange + ":" + inst.TradingSymbol
		if _, exists := bySym[key]; exists {
			continue
		}
		bySym[key] = inst
		all = append(all, inst)
	}

	if len(all) == 0 {
		return errors.New("instruments: no rows matched the configured segments")
	}

	s.mu.Lock()
	s.all, s.bySym, s.derivatives, s.loaded, s.loadErr = all, bySym, hasDerivatives, time.Now(), nil
	s.mu.Unlock()
	return nil
}

func (s *Store) setErr(err error) {
	s.mu.Lock()
	s.loadErr = err
	s.mu.Unlock()
}

// Universe returns the NSE cash instruments that also have listed F&O
// contracts — the standard liquidity screen (~200 names). This is the set the
// analytics scanner works over: liquid enough that a paper fill at LTP is not
// a fantasy.
func (s *Store) Universe() []Instrument {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []Instrument
	for _, inst := range s.all {
		if inst.Exchange == "NSE" && s.derivatives[inst.TradingSymbol] && (inst.Series == "EQ" || inst.Series == "BE") {
			out = append(out, inst)
		}
	}
	return out
}

// Lookup finds one instrument by exchange + trading symbol.
func (s *Store) Lookup(exchange, symbol string) (Instrument, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.bySym[strings.ToUpper(exchange)+":"+strings.ToUpper(symbol)]
	return inst, ok
}

// Search ranks instruments against a query, best match first.
//
// Ranking is deliberately simple and predictable: an exact ticker beats a
// ticker prefix, which beats a name prefix, which beats a substring anywhere.
// Equity series (EQ/BE) float above bonds and other paper, because that is what
// someone typing "REL" is nearly always after.
//
// exchange filters the result set; pass "" for every exchange. The dashboard
// pins it to NSE, because trading is NSE-cash only in v1 and returning a BSE
// row the order path would then route to NSE would be a lie.
func (s *Store) Search(query, exchange string, limit int) []Instrument {
	query = strings.ToUpper(strings.TrimSpace(query))
	exchange = strings.ToUpper(strings.TrimSpace(exchange))
	if query == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type scored struct {
		inst  Instrument
		score int
	}
	var hits []scored

	for _, inst := range s.all {
		if exchange != "" && inst.Exchange != exchange {
			continue
		}
		upperName := strings.ToUpper(inst.Name)

		score := 0
		switch {
		case inst.TradingSymbol == query:
			score = 100
		case strings.HasPrefix(inst.TradingSymbol, query):
			score = 80
		case upperName == query:
			score = 75
		case strings.HasPrefix(upperName, query):
			score = 60
		case strings.Contains(inst.TradingSymbol, query):
			score = 40
		case strings.Contains(upperName, query):
			score = 20
		default:
			continue
		}

		// A listed derivative is the best liquidity signal the master carries,
		// and it is what separates the household name from the lookalike.
		if s.derivatives[inst.TradingSymbol] {
			score += 25
		}
		// Prefer ordinary equity over debentures, ETFs of the same name, etc.
		if inst.Series == "EQ" || inst.Series == "BE" {
			score += 10
		}

		hits = append(hits, scored{inst: inst, score: score})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		// Within a tier, the tightest match to what was typed wins: "Tata
		// Motors" should beat "Tata Motors Commercial".
		if len(hits[i].inst.Name) != len(hits[j].inst.Name) {
			return len(hits[i].inst.Name) < len(hits[j].inst.Name)
		}
		return hits[i].inst.TradingSymbol < hits[j].inst.TradingSymbol
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Instrument, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.inst)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
