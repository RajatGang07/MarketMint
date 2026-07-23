// Package signals condenses everything the platform knows — positional
// momentum rank, today's intraday breakout state, and the account's actual
// holdings — into one table with a single verdict per stock: BUY, SELL,
// WATCH or HOLD. Every verdict carries its reasons in plain language; the
// table never asks to be trusted on authority.
//
// The vocabulary is deliberately narrow:
//
//	BUY   — top of the momentum ranking AND a plan fits the risk bands.
//	SELL  — applies only to holdings (the engine is long-only): the momentum
//	        rank has collapsed, RSI signals a blow-off, or the position is
//	        losing with no protective stop resting.
//	WATCH — worth attention, not money: ranks just below the buy cut, or a
//	        stock that broke out intraday today.
//	HOLD  — a holding that is doing fine (ideally with its exits armed).
package signals

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/gangrajat/groww-paper-trading/backend/internal/analytics"
	"github.com/gangrajat/groww-paper-trading/backend/internal/intraday"
	"github.com/gangrajat/groww-paper-trading/backend/internal/marketdata"
	"github.com/gangrajat/groww-paper-trading/backend/internal/store"
)

// Verdict thresholds, in one place so the docs and code cannot drift.
const (
	buyRankCut   = 10   // BUY: rank ≤ 10 (and a plan must fit)
	watchRankCut = 25   // WATCH: rank ≤ 25
	rsiBlowOff   = 80.0 // SELL a holding when RSI runs past this
	undefendedPL = -8.0 // SELL a holding down this % with no stop resting
)

// Row is one line of the board.
type Row struct {
	Action    string   `json:"action"` // BUY / SELL / WATCH / HOLD
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name,omitempty"`
	LastPrice float64  `json:"last_price"`
	ChangePct float64  `json:"change_pct"`
	Rank      int      `json:"rank,omitempty"`
	Score     float64  `json:"score,omitempty"`
	Reasons   []string `json:"reasons"`

	// BUY rows: the sized plan.
	Plan *analytics.Plan `json:"plan,omitempty"`

	// Holding rows (SELL / HOLD).
	HeldQuantity  int64   `json:"held_quantity,omitempty"`
	AvgPrice      float64 `json:"avg_price,omitempty"`
	UnrealizedPnL float64 `json:"unrealized_pnl,omitempty"`
	UnrealizedPct float64 `json:"unrealized_pct,omitempty"`
	ExitsArmed    bool    `json:"exits_armed,omitempty"`
}

// Board is the whole table plus its provenance.
type Board struct {
	AsOf        time.Time      `json:"as_of"`
	Rows        []Row          `json:"rows"`
	Counts      map[string]int `json:"counts"`
	Universe    int            `json:"universe_size"`
	PriceSource string         `json:"price_source"`
	SessionOpen bool           `json:"session_open"`
	Caveats     []string       `json:"caveats"`
}

// Composer wires the two scanners and the store together.
type Composer struct {
	analytics *analytics.Engine
	intraday  *intraday.Scanner
	store     *store.Store
	market    marketdata.Provider
}

func New(a *analytics.Engine, i *intraday.Scanner, st *store.Store, m marketdata.Provider) *Composer {
	return &Composer{analytics: a, intraday: i, store: st, market: m}
}

// Compose builds the board for one account.
func (c *Composer) Compose(ctx context.Context, accountID int64, bands analytics.RiskBands, availableCash float64) (*Board, error) {
	ranked, scanMeta, err := c.analytics.Scored(ctx)
	if err != nil {
		return nil, err
	}
	intr, err := c.intraday.Scan(ctx, 5000, availableCash, 0)
	if err != nil {
		// Intraday colour is enrichment, not a dependency; the board still
		// stands on the positional ranking.
		intr = &intraday.Result{}
	}
	positions, err := c.store.ListPositions(ctx, accountID)
	if err != nil {
		return nil, err
	}
	orders, err := c.store.ListOrders(ctx, accountID, 200)
	if err != nil {
		return nil, err
	}

	byRank := make(map[string]analytics.Candidate, len(ranked))
	for _, cand := range ranked {
		byRank[cand.Symbol] = cand
	}
	orb := make(map[string]intraday.Pick, len(intr.Picks))
	for _, p := range intr.Picks {
		orb[p.Symbol] = p
	}
	// A holding counts as protected when any SELL exit is still resting.
	protected := make(map[string]bool)
	for _, o := range orders {
		if o.TransactionType == "SELL" && o.Status == "OPEN" {
			protected[o.TradingSymbol] = true
		}
	}
	held := make(map[string]store.Position, len(positions))
	for _, p := range positions {
		if p.Quantity > 0 {
			held[p.TradingSymbol] = p
		}
	}

	universeN := len(ranked)
	var rows []Row

	// --- Holdings first: SELL or HOLD -----------------------------------
	for _, pos := range positions {
		if pos.Quantity <= 0 {
			continue
		}
		cand, scored := byRank[pos.TradingSymbol]
		row := holdingRow(pos, cand, scored, universeN, protected[pos.TradingSymbol])
		if note, ok := orbNote(orb, pos.TradingSymbol); ok {
			row.Reasons = append(row.Reasons, note)
		}
		rows = append(rows, row)
	}

	// --- Fresh money: BUY and WATCH --------------------------------------
	for _, cand := range ranked {
		if cand.Rank > watchRankCut {
			break // ranked list is ordered; nothing actionable further down
		}
		if _, isHeld := held[cand.Symbol]; isHeld {
			continue // the holding row already covers it
		}

		row := Row{
			Symbol:    cand.Symbol,
			Name:      cand.Name,
			Rank:      cand.Rank,
			Score:     round2(cand.Score),
			LastPrice: cand.Features.LastClose,
		}

		if cand.Rank <= buyRankCut {
			if plan, ok := analytics.PlanFor(cand, bands, availableCash); ok {
				row.Action = "BUY"
				row.Plan = &plan
				row.Reasons = append(row.Reasons,
					fmt.Sprintf("momentum rank #%d of %d (3-mo %+.1f%%, 1-mo %+.1f%%)",
						cand.Rank, universeN, cand.Features.Momentum60*100, cand.Features.Momentum20*100))
				if plan.CapitalCapped {
					row.Reasons = append(row.Reasons, "sized down to available cash — loss/profit run below the requested bands")
				}
			} else {
				row.Action = "WATCH"
				row.Reasons = append(row.Reasons,
					fmt.Sprintf("rank #%d, but no whole-share size fits the loss band at this volatility", cand.Rank))
			}
		} else {
			row.Action = "WATCH"
			row.Reasons = append(row.Reasons, fmt.Sprintf("momentum rank #%d of %d — just below the buy cut", cand.Rank, universeN))
		}

		if cand.Features.RSI14 > 72 {
			row.Reasons = append(row.Reasons, fmt.Sprintf("RSI %.0f — extended, prefer a pullback entry", cand.Features.RSI14))
		}
		if note, ok := orbNote(orb, cand.Symbol); ok {
			row.Reasons = append(row.Reasons, note)
		}
		rows = append(rows, row)
	}

	// --- Intraday breakouts that the positional ranking ignores ----------
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		seen[r.Symbol] = true
	}
	for _, p := range intr.Picks {
		if seen[p.Symbol] || p.Status != "ACTIVE" {
			continue
		}
		cand := byRank[p.Symbol]
		rows = append(rows, Row{
			Action:    "WATCH",
			Symbol:    p.Symbol,
			Name:      p.Name,
			Rank:      cand.Rank,
			LastPrice: p.LastPrice,
			Reasons: []string{
				fmt.Sprintf("intraday ORB breakout running since %s (entry %.2f, %.1fx volume)",
					p.EntryTime.Format("15:04"), p.Entry, p.RVOL),
				"not in the positional buy zone — intraday tactics only",
			},
		})
	}

	c.fillQuotes(ctx, rows)
	sortRows(rows)

	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Action]++
	}

	return &Board{
		AsOf:        time.Now(),
		Rows:        rows,
		Counts:      counts,
		Universe:    universeN,
		PriceSource: scanMeta.PriceSource,
		SessionOpen: intr.SessionOpen,
		Caveats: []string{
			"Verdicts are rule-outputs from the momentum ranking, today's intraday state and your holdings — reasons are listed per row.",
			"SELL applies to holdings only; the paper engine is long-only.",
			"The positional model's edge is modest (see Trade ideas backtest); position sizing and stops carry the risk.",
		},
	}, nil
}

// holdingRow decides SELL vs HOLD for one position. Pure, so it is testable.
func holdingRow(pos store.Position, cand analytics.Candidate, scored bool, universeN int, exitsArmed bool) Row {
	avg, _ := pos.AvgPrice.Float64()
	row := Row{
		Symbol:       pos.TradingSymbol,
		Name:         cand.Name,
		HeldQuantity: pos.Quantity,
		AvgPrice:     round2(avg),
		ExitsArmed:   exitsArmed,
		LastPrice:    cand.Features.LastClose,
	}

	// Mark to the model's last close when scored; the quote pass refreshes it.
	last := cand.Features.LastClose
	if last <= 0 {
		last = avg
	}
	row.UnrealizedPnL = round2(float64(pos.Quantity) * (last - avg))
	if avg > 0 {
		row.UnrealizedPct = round2((last/avg - 1) * 100)
	}

	var reasons []string
	if scored {
		row.Rank = cand.Rank
		row.Score = round2(cand.Score)
		if cand.Rank > universeN*2/3 {
			reasons = append(reasons, fmt.Sprintf("momentum rank collapsed to #%d of %d — the trend this was bought for is gone", cand.Rank, universeN))
		}
		if cand.Features.RSI14 > rsiBlowOff {
			reasons = append(reasons, fmt.Sprintf("RSI %.0f — blow-off territory, consider locking gains", cand.Features.RSI14))
		}
	} else {
		reasons = append(reasons, "not scored by the model (outside the F&O universe or screened out on liquidity/RSI) — no model opinion")
	}
	if !exitsArmed && row.UnrealizedPct < undefendedPL {
		reasons = append(reasons, fmt.Sprintf("down %.1f%% with no stop resting — an undefended loser", row.UnrealizedPct))
	}

	// Any concrete deterioration reason ⇒ SELL; otherwise HOLD.
	sell := false
	for _, r := range reasons {
		if r != "not scored by the model (outside the F&O universe or screened out on liquidity/RSI) — no model opinion" {
			sell = true
		}
	}
	if sell {
		row.Action = "SELL"
		row.Reasons = reasons
		return row
	}

	row.Action = "HOLD"
	row.Reasons = reasons
	if exitsArmed {
		row.Reasons = append(row.Reasons, "exits armed — stop/target resting server-side")
	} else {
		row.Reasons = append(row.Reasons, "no exit orders resting — consider adding a bracket")
	}
	return row
}

func orbNote(orb map[string]intraday.Pick, symbol string) (string, bool) {
	p, ok := orb[symbol]
	if !ok {
		return "", false
	}
	switch p.Status {
	case "ACTIVE":
		return fmt.Sprintf("intraday: ORB breakout running since %s", p.EntryTime.Format("15:04")), true
	case "TARGET":
		return "intraday: hit its 2R target today", true
	case "TRAIL":
		return "intraday: broke out today, trailed out", true
	case "STOP":
		return "intraday: broke out today but stopped — weak follow-through", true
	default:
		return "", false
	}
}

// fillQuotes refreshes last price and day change for the final rows, a few at
// a time. A miss keeps the model's close — never blank.
func (c *Composer) fillQuotes(ctx context.Context, rows []Row) {
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(r *Row) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			q, err := c.market.Quote(ctx, "NSE", "CASH", r.Symbol)
			if err != nil {
				return
			}
			if lp, _ := q.LastPrice.Float64(); lp > 0 {
				r.LastPrice = round2(lp)
				if r.HeldQuantity > 0 && r.AvgPrice > 0 {
					r.UnrealizedPnL = round2(float64(r.HeldQuantity) * (lp - r.AvgPrice))
					r.UnrealizedPct = round2((lp/r.AvgPrice - 1) * 100)
				}
			}
			pct, _ := q.ChangePct().Float64()
			r.ChangePct = round2(pct)
		}(&rows[i])
	}
	wg.Wait()
}

// sortRows orders the board for action: BUY, SELL, WATCH, then HOLD.
func sortRows(rows []Row) {
	weight := map[string]int{"BUY": 0, "SELL": 1, "WATCH": 2, "HOLD": 3}
	sort.SliceStable(rows, func(i, j int) bool {
		if weight[rows[i].Action] != weight[rows[j].Action] {
			return weight[rows[i].Action] < weight[rows[j].Action]
		}
		ri, rj := rows[i].Rank, rows[j].Rank
		if ri == 0 {
			ri = 1 << 20
		}
		if rj == 0 {
			rj = 1 << 20
		}
		if ri != rj {
			return ri < rj
		}
		return rows[i].Symbol < rows[j].Symbol
	})
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
