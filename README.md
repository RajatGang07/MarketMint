# Groww Paper Trading

Simulated (paper) trading on top of Groww's Trading API. Real prices where the
account is entitled to them, a deterministic simulator where it isn't, and a
virtual cash account that books positions, fills and P&L exactly like a broker
would — without ever sending an order to an exchange.

**Stack:** Go (chi + pgx) · React + TypeScript + Vite + Tailwind · PostgreSQL 16

```
backend/       Go API — market data, paper engine, REST
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

## Market data, and an important caveat

Groww sells **Live Data as a separate subscription**. The API key used to build
this answers `403 Access forbidden` on every `/live-data/*` endpoint while
order, holdings, positions and margin endpoints work fine — the minted token's
roles are `order-basic, non_trading-basic, order_read_only-basic`, with no
live-data role.

So the platform ships with three modes, set by `MARKET_DATA_MODE`:

| Mode   | Behaviour |
| ------ | --------- |
| `auto` | **Default.** Try Groww; fall back to the simulator on any failure, and re-probe every 5 minutes so a subscription enabled later is picked up without a restart. |
| `live` | Groww only. Errors surface instead of being masked — use this to confirm entitlement. |
| `mock` | Always simulate. No network calls to Groww at all. |

`GET /health` reports which source is actually serving prices and, when
degraded, why. The dashboard shows the same thing as a badge and a banner, so
you are never quietly trading against fake prices without knowing.

The simulator gives each symbol a stable base price (well-known NSE tickers use
realistic values) and moves it on a slow ±1% sine with a little tick noise —
enough for resting limit orders to actually get hit.

**To switch to real prices:** enable the Live Data subscription on the Groww
account, then restart with `MARKET_DATA_MODE=live` and check `/health`.

### Authentication

Groww access tokens expire daily at 06:00 IST. Give the server
`GROWW_API_KEY` + `GROWW_API_SECRET` and it mints and refreshes tokens itself
via the checksum handshake (`SHA256(secret + epochSeconds)` →
`POST /v1/token/api/access`). `GROWW_ACCESS_TOKEN` is supported as an
alternative but is used verbatim and never refreshed, so it dies overnight.

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

### Out of scope in v1

Long-only cash equities. No shorting, no F&O margining, no brokerage/STT/GST
charges (fills are at a clean price), and a single account with no auth. The
places these would slot in are marked in the code.

---

## API

| Method | Path | Purpose |
| ------ | ---- | ------- |
| `GET`  | `/health` | Status, active market-data mode, degradation reason |
| `GET`  | `/market/ltp?symbols=RELIANCE,TCS` | Last traded prices |
| `GET`  | `/market/quote?symbol=RELIANCE` | Full quote with OHLC |
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
(`MARKET`) default if omitted. `limit_price` is required for `LIMIT`.

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
# MarketMint
