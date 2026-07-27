// Package news pulls recent headlines for one stock and scores their
// sentiment. It is strictly best-effort: no key, no internet, or a dead feed
// degrades to "no news available" instead of failing the forecast.
//
// Two scoring paths, best available wins:
//
//	claude   headlines are classified by Claude in one API call. Needs
//	         ANTHROPIC_API_KEY. Real NLP: understands negation, context,
//	         "falls less than feared" style headlines.
//	lexicon  a finance keyword list. Crude but free and offline-friendly
//	         once headlines are fetched.
//
// Headlines come from Google News RSS (no key required). Results are cached
// per symbol for a few minutes so a dashboard refresh does not re-hit either
// service.
package news

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Item is one scored headline.
type Item struct {
	Title     string    `json:"title"`
	Source    string    `json:"source,omitempty"`
	URL       string    `json:"url,omitempty"`
	Published time.Time `json:"published"`
	// Sentiment is -1 (very negative) .. +1 (very positive).
	Sentiment float64 `json:"sentiment"`
}

// Result is everything the forecast needs from the news layer.
type Result struct {
	Items []Item `json:"items"`
	// Overall is the recency-weighted mean sentiment, -1..+1.
	Overall float64 `json:"overall"`
	// Method reports how sentiment was scored: "claude", "lexicon" or "none".
	Method  string   `json:"method"`
	Caveats []string `json:"caveats,omitempty"`
}

const (
	maxHeadlines = 12
	cacheTTL     = 5 * time.Minute
	feedTimeout  = 8 * time.Second
)

// Fetcher fetches and scores news. Safe for concurrent use.
type Fetcher struct {
	http         *http.Client
	log          *slog.Logger
	anthropicKey string
	model        string

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at  time.Time
	res Result
}

// New builds a Fetcher. anthropicKey may be empty — the lexicon then scores
// alone. model falls back to a sensible default when empty.
func New(anthropicKey, model string, log *slog.Logger) *Fetcher {
	if model == "" {
		model = "claude-opus-5"
	}
	return &Fetcher{
		http:         &http.Client{Timeout: feedTimeout},
		log:          log,
		anthropicKey: anthropicKey,
		model:        model,
		cache:        make(map[string]cached),
	}
}

// ForSymbol returns scored news for one stock. name may be empty; the symbol
// is always part of the query.
func (f *Fetcher) ForSymbol(ctx context.Context, symbol, name string) Result {
	f.mu.Lock()
	if c, ok := f.cache[symbol]; ok && time.Since(c.at) < cacheTTL {
		f.mu.Unlock()
		return c.res
	}
	f.mu.Unlock()

	res := f.fetch(ctx, symbol, name)

	f.mu.Lock()
	f.cache[symbol] = cached{at: time.Now(), res: res}
	f.mu.Unlock()
	return res
}

func (f *Fetcher) fetch(ctx context.Context, symbol, name string) Result {
	items, err := f.headlines(ctx, symbol, name)
	if err != nil {
		return Result{Method: "none", Caveats: []string{"News unavailable: " + err.Error()}}
	}
	if len(items) == 0 {
		return Result{Method: "none", Caveats: []string{"No recent headlines found for " + symbol + "."}}
	}

	method := "lexicon"
	caveats := []string{"Lexicon sentiment is keyword-based and can misread nuanced headlines."}
	if f.anthropicKey != "" {
		if err := f.scoreWithClaude(ctx, items); err != nil {
			f.log.Warn("claude sentiment failed; falling back to lexicon", "err", err)
			caveats = append(caveats, "Claude scoring failed ("+err.Error()+"); used keyword lexicon instead.")
			scoreWithLexicon(items)
		} else {
			method = "claude"
			caveats = nil
		}
	} else {
		scoreWithLexicon(items)
		caveats = append(caveats, "Set ANTHROPIC_API_KEY for higher-quality sentiment.")
	}

	return Result{Items: items, Overall: overall(items), Method: method, Caveats: caveats}
}

// overall is a recency-weighted mean: a headline from an hour ago matters
// more than one from five days ago (half-life ~24h).
func overall(items []Item) float64 {
	var num, den float64
	now := time.Now()
	for _, it := range items {
		age := now.Sub(it.Published).Hours()
		if age < 0 {
			age = 0
		}
		w := 1.0 / (1.0 + age/24.0)
		num += w * it.Sentiment
		den += w
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// ---------------------------------------------------------------------------
// Headlines: Google News RSS
// ---------------------------------------------------------------------------

type rss struct {
	Channel struct {
		Items []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			PubDate string `xml:"pubDate"`
			Source  string `xml:"source"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (f *Fetcher) headlines(ctx context.Context, symbol, name string) ([]Item, error) {
	query := symbol + " stock"
	if name != "" {
		query = `"` + name + `" OR ` + symbol + ` stock`
	}
	u := "https://news.google.com/rss/search?q=" + url.QueryEscape(query) + "&hl=en-IN&gl=IN&ceid=IN:en"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "marketmint/1.0")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("news feed answered %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}

	items := make([]Item, 0, maxHeadlines)
	for _, it := range feed.Channel.Items {
		if len(items) == maxHeadlines {
			break
		}
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		// Google News suffixes " - Source"; strip it when the source tag agrees.
		if it.Source != "" {
			title = strings.TrimSuffix(title, " - "+it.Source)
		}
		pub, _ := time.Parse(time.RFC1123, it.PubDate)
		items = append(items, Item{Title: title, Source: it.Source, URL: it.Link, Published: pub})
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Scoring: Claude
// ---------------------------------------------------------------------------

// scoreWithClaude classifies every headline in one request. The model answers
// a bare JSON array of floats so parsing stays trivial.
func (f *Fetcher) scoreWithClaude(ctx context.Context, items []Item) error {
	client := anthropic.NewClient(option.WithAPIKey(f.anthropicKey))

	var sb strings.Builder
	sb.WriteString("Score each numbered news headline for its likely short-term impact on the stock's price, ")
	sb.WriteString("from -1.0 (very negative) to 1.0 (very positive), 0 for neutral or irrelevant. ")
	sb.WriteString("Respond with ONLY a JSON array of numbers, one per headline, in order.\n\n")
	for i, it := range items {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, it.Title)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(f.model),
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(sb.String())),
		},
	})
	if err != nil {
		return err
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return fmt.Errorf("model declined the request")
	}

	var text string
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += t.Text
		}
	}
	scores, err := parseScores(text, len(items))
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Sentiment = clamp(scores[i], -1, 1)
	}
	return nil
}

var jsonArrayRe = regexp.MustCompile(`\[[^\[\]]*\]`)

// parseScores digs the JSON array out of the reply, tolerating prose around it.
func parseScores(text string, want int) ([]float64, error) {
	raw := jsonArrayRe.FindString(text)
	if raw == "" {
		return nil, fmt.Errorf("no JSON array in model reply")
	}
	var scores []float64
	if err := json.Unmarshal([]byte(raw), &scores); err != nil {
		return nil, fmt.Errorf("parse scores: %w", err)
	}
	if len(scores) != want {
		return nil, fmt.Errorf("got %d scores for %d headlines", len(scores), want)
	}
	return scores, nil
}

// ---------------------------------------------------------------------------
// Scoring: keyword lexicon
// ---------------------------------------------------------------------------

// A small finance-flavoured lexicon. Deliberately conservative: a headline
// scores by counting hits, then squashing into -1..+1.
var (
	positiveWords = []string{
		"surge", "soar", "jump", "rally", "gain", "record high", "beats", "beat estimates",
		"upgrade", "upgraded", "outperform", "profit rises", "profit jumps", "strong results",
		"buyback", "bonus", "dividend", "order win", "wins order", "contract", "expansion",
		"growth", "bullish", "all-time high", "raises guidance", "approval", "launches",
	}
	negativeWords = []string{
		"fall", "falls", "drop", "plunge", "crash", "slump", "sink", "tumble", "loss",
		"misses", "missed estimates", "downgrade", "downgraded", "underperform", "probe",
		"investigation", "fraud", "penalty", "fine", "lawsuit", "recall", "layoff", "strike",
		"default", "debt", "bearish", "weak results", "cuts guidance", "resigns", "sell-off",
		"scam", "raid", "shutdown",
	}
)

func scoreWithLexicon(items []Item) {
	for i := range items {
		items[i].Sentiment = lexiconScore(items[i].Title)
	}
}

func lexiconScore(title string) float64 {
	t := strings.ToLower(title)
	score := 0
	for _, w := range positiveWords {
		if strings.Contains(t, w) {
			score++
		}
	}
	for _, w := range negativeWords {
		if strings.Contains(t, w) {
			score--
		}
	}
	// Squash: one hit = ±0.4, two = ±0.7, three+ = ±0.9.
	switch {
	case score >= 3:
		return 0.9
	case score == 2:
		return 0.7
	case score == 1:
		return 0.4
	case score == -1:
		return -0.4
	case score == -2:
		return -0.7
	case score <= -3:
		return -0.9
	}
	return 0
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
