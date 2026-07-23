# MarketMint — Groww Paper Trading

A paper-trading desk for Indian equities: live NSE prices, real symbol search
over the whole cash universe, charts, and a simulated broker that books
positions, fills and P&L against a virtual cash account — without ever sending
an order to an exchange.

**Stack:** Go (chi + pgx) · React + TypeScript + Vite + Tailwind · PostgreSQL 16

```
backend/       Go API — market data chain, instrument universe, paper engine, REST
frontend/      React dashboard
legacy-python/ The earlier FastAPI prototype, kept only as reference. Safe to delete.
```

---

## Quick start

Prerequisites: Go 1.24+, Node 20+, PostgreSQL 16.

```bash
# 1. Database
createuser paper --login --pwprompt      # password: paper
createdb paper_trading --owner paper

# 2. Backend  (schema is created automatically on first boot)
cd backend
cp .env.example .env                     # fill in GROWW_API_KEY / GROWW_API_SECRET
go run ./cmd/server                      # http://localhost:8000

# 3. Frontend
cd ../frontend
npm install
npm run dev                              # http://localhost:5173
```

---

## The dashboard

Two tabs. **Trade**: search (press `/`), watchlist, quote header, chart
(1D–1Y with crosshair + prev-close reference), order ticket with live
buying-power checks, KPI cards, and the blotter (positions / working orders /
history / trades). **Ideas & Signals**: sub-tabs for the Signals board, the
Intraday scanner and Positional ideas — each loads itself on first open and
shows when it was last updated.

Everything that moves money goes through a confirmation dialog showing the
full plan (entry, stop, target, quantity, rupees at risk); results arrive as
toasts. Clicking a symbol anywhere opens it on the Trade tab. The header shows
market open/closed (IST), the live-data source, and equity + total P&L at all
times. Prices and the account poll every 5s; the chart every 30s.

---

## Market data

Prices come from an **ordered provider chain** (`MARKET_DATA_PROVIDERS`,
default `groww,yahoo,mock`). The first source that answers wins; a failing one
is skipped for two minutes and then re-probed, so a subscription enabled later
is picked up without a restart.

| Provider | Notes |
| -------- | ----- |
| `groww` | The real broker feed. **Requires a Live Data subscription on the Groww account.** |
| `yahoo` | Public NSE/BSE quotes and history. No key, no subscription. What this build actually runs on. |
| `mock` | Deterministic simulator — stable base prices on a slow ±1% sine. Never fails; keep it last as the safety net, or drop it if you would rather the API error than quietly simulate. |

### The Groww Live Data caveat

The API key this was built with authenticates fine and can read orders,
holdings, positions and margins — but every `/live-data/*` and `/historical/*`
call answers `403 Access forbidden`. The minted token's roles are
`order-basic, non_trading-basic, order_read_only-basic`, with no live-data role;
Groww sells Live Data as a separate subscription.

That is why the chain exists. Enable the subscription and `groww` starts
winning the chain automatically — no code change. To confirm entitlement, set
`MARKET_DATA_PROVIDERS=groww` and watch `/health` fail loudly instead of
falling through.

`GET /health` always reports which provider is actually serving prices and why
the others aren't. The dashboard shows the same thing as a badge, so you can
never be trading against simulated prices without knowing.

### Authentication

Groww access tokens expire daily at 06:00 IST. Give the server
`GROWW_API_KEY` + `GROWW_API_SECRET` and it mints and refreshes tokens itself
via the checksum handshake (`SHA256(secret + epochSeconds)` →
`POST /v1/token/api/access`). `GROWW_ACCESS_TOKEN` is supported as an
alternative but is used verbatim and never refreshed, so it dies overnight.

### Instrument universe

Groww also publishes the full instrument master as a **public CSV** — no auth,
no subscription. The server downloads it at boot (caching for 12 hours), keeps
the cash segment, and serves ranked search from memory. Ranking prefers exact
tickers, then ticker prefixes, then company names; symbols with listed F&O
contracts get a boost, which is the best liquidity proxy the file carries and is
what makes "REL" return RELIANCE rather than RELAXO.

---

## The prediction model (Trade ideas)

`GET /analytics/recommendations` — or the **Trade ideas** panel in the
dashboard — ranks the ~210-stock NSE F&O universe on the last **3 months of
daily bars** and returns risk-sized picks.

**Score** = weighted cross-sectional z-scores: 3-month momentum (0.30),
1-month momentum (0.20), trend persistence — % of sessions above the 20-DMA
(0.15), proximity to the 60d high (0.15), volume expansion (0.10), and an RSI
health band centred on 60 (0.10). Hard screens: price ≥ ₹50, 20d turnover ≥
₹5cr, RSI ≤ 80. Every response includes the per-feature contributions.

**Sizing** comes straight from the brief "lose ₹20–30k, make ₹30–50k":
stop = entry − 2×ATR(14), target = entry + 3.2×ATR (the 1.6 risk:reward those
bands imply), quantity = ₹25k ÷ per-share risk, capped by free cash. Bands are
tunable per request (`?loss_min=…&loss_max=…&profit_min=…&profit_max=…`).

**Backtest, in every response:** the identical rule is replayed at 8 monthly
folds over the fetched year (90d features → next 21d return). Typical output:
rank-IC ≈ 0.07, top-decile hit rate ≈ 54%, top-decile mean ≈ +1.4%/month vs
+0.2% for the universe. **That is a mild tilt, not a forecast** — sizing and
stops are what manage the downside, and the UI says so out loud.

**Brackets:** a BUY may carry `stop_loss` + `target`. Once it fills, the
engine rests a stop-market (`SL`) sell at the stop and a `LIMIT` sell at the
target as an **OCO pair** — whichever fills first cancels the other. The SL is
a stop-*market*: it triggers at the level but fills at the prevailing price,
so gap risk behaves like the real thing.

---

## The signals board

`GET /analytics/signals` — the **Signals** table at the top of the dashboard —
collapses everything above into **one verdict per stock**: `BUY`, `SELL`,
`WATCH` or `HOLD`, each with its reasons spelled out on the row.

| Verdict | Rule |
| ------- | ---- |
| `BUY` | Momentum rank ≤ 10 and a plan fits the risk bands (plan shown on the row; one click places it with the bracket). |
| `SELL` | Holdings only (long-only engine): momentum rank collapsed to the bottom tercile, RSI > 80 blow-off, or the position is down > 8% with **no stop resting** — an undefended loser. The Sell button cancels the resting exits first so the bracket cannot double-sell. |
| `WATCH` | Ranks 11–25, top-rank names too hot/coarse to size, and any stock whose intraday ORB breakout is running right now. |
| `HOLD` | A holding the rules have nothing against — annotated with whether its exits are armed. |

Rows are enriched with the day's intraday state ("broke out today, trailed
out", "hit its 2R target today") so the positional and intraday views land in
one place.

---

## The intraday scanner

`GET /analytics/intraday` — or the **Intraday scanner** panel — hunts
opening-range breakouts on 5-minute bars across the same F&O universe, and
answers both halves of the intraday question: *what to buy* and *exactly when
to exit*.

**The rule (long-only, one trade per symbol per day):** opening range = first
15 minutes. Enter when a 5m bar *closes* above the OR high before 14:30, on
volume ≥ 1.5× the session's average, with price above VWAP. Stop = the higher
of (OR low, entry − 1.5×ATR), risk floored at 0.35%. Target = entry + 2R.
The stop **trails the session high by 1R**, so after a 1R run the trade cannot
lose and a 2R run locks at least +1R. Whatever is still open **squares off at
15:15 IST** — nothing is held overnight. Default sizing risks ₹5,000 per trade
(`?risk=` to change), capped at 25% of free cash per idea.

**Exits are enforced server-side.** An intraday buy goes out with product
`MIS`, a trailing `SL` and a 2R `LIMIT` as an OCO pair; the matcher ratchets
the trail every 5s and force-closes any surviving MIS exit in the 15:15–15:40
window — even with the browser closed.

**The honest numbers** (same rule replayed over ~20 sessions × 210 stocks,
~2,100 simulated trades, conservative fills, no costs): win rate ≈ 38%,
average ≈ +0.03R, profit factor ≈ 1.08. **The raw edge is thin** — wins
average +1.06R against −0.61R losses, which is the asymmetry doing the work.
The per-symbol history column exists precisely so you trade the names where
the rule has actually paid (the ranking uses it), skip the chop, and never
widen a stop.

---

## What the engine does

- **MARKET orders** fill immediately at the last traded price.
- **LIMIT orders** fill when marketable, at the better of the limit and the
  market. Otherwise they rest as `OPEN` and a background matcher
  (`MATCH_INTERVAL`, default 5s) retries them until they fill or are cancelled.
- **Positions** carry a weighted-average cost basis. Selling books realised P&L
  and returns the proceeds to cash.
- **Rejections are recorded, not thrown away** — an order with insufficient
  funds or shares is persisted with status `REJECTED` and the reason in
  `message`, which is how a real broker reports it.
- **Money is `NUMERIC` in Postgres and `decimal.Decimal` in Go.** No floats
  anywhere in the money path.
- Cash and position updates run inside a transaction with the account row
  locked `FOR UPDATE`, so concurrent orders can't spend the same rupee twice.

Note that outside market hours live prices are frozen at the close, so a buy
and a sell in the same session will both fill at the same price and show zero
P&L. That is the feed being honest, not the engine being wrong.

### Out of scope in v1

Long-only NSE cash equities. No shorting, no F&O margining, no
brokerage/STT/GST charges (fills are at a clean price), and a single account
with no auth. The places these would slot in are marked in the code.

---

## API

| Method | Path | Purpose |
| ------ | ---- | ------- |
| `GET`  | `/health` | Status, active price source, per-provider health, instrument count |
| `GET`  | `/market/ltp?symbols=RELIANCE,TCS` | Last traded prices |
| `GET`  | `/market/quote?symbol=RELIANCE` | One quote with OHLC and day change |
| `GET`  | `/market/quotes?symbols=RELIANCE,TCS` | Bulk quotes for a watchlist |
| `GET`  | `/market/candles?symbol=RELIANCE&range=1d` | OHLCV bars (`1d`, `5d`, `1mo`, `3mo`, `1y`) |
| `GET`  | `/instruments/search?q=reliance` | Ranked instrument search |
| `GET`  | `/analytics/recommendations` | Momentum screen + risk-sized picks + backtest |
| `GET`  | `/analytics/intraday` | ORB intraday scanner + per-symbol history + backtest |
| `GET`  | `/analytics/signals` | One-verdict-per-stock board: BUY / SELL / WATCH / HOLD |
| `GET`  | `/portfolio` | Holdings marked to market + equity roll-up |
| `POST` | `/portfolio/reset` | Wipe history, restore starting cash |
| `GET`  | `/orders` | Order history (newest first) |
| `POST` | `/orders` | Place an order |
| `POST` | `/orders/{orderRef}/cancel` | Cancel a resting order |
| `GET`  | `/trades` | Executed fills with realised P&L |

Errors come back as `{"detail": "..."}` with a 4xx/5xx status.

```bash
curl -X POST localhost:8000/orders \
  -H 'Content-Type: application/json' \
  -d '{"trading_symbol":"RELIANCE","transaction_type":"BUY","order_type":"MARKET","quantity":10}'
```

`exchange` (`NSE`), `segment` (`CASH`), `product` (`CNC`) and `order_type`
(`MARKET`) default if omitted. `limit_price` is required for `LIMIT`;
`trigger_price` is required for `SL`. Adding `stop_loss` + `target` to a BUY
creates the OCO bracket described above; `trail_by` makes that stop trail the
high-water mark, and `product: "MIS"` opts the exits into the 15:15 square-off.

---

## Configuration

Everything is environment-driven; see [backend/.env.example](backend/.env.example)
for the annotated list. The frontend reads `VITE_API_BASE`
(see [frontend/.env.example](frontend/.env.example)), defaulting to
`http://localhost:8000`.

## Tests

```bash
cd backend  && go test ./...
cd frontend && npm run build   # typecheck + bundle
```

## Security note

`backend/.env` holds live Groww credentials and is gitignored. Rotate the API
secret if it has ever been pasted into a chat, a ticket, or a shared document.
