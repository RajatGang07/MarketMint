// Package store owns the Postgres schema, the domain rows, and every query the
// app runs. Nothing above this package writes SQL.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned by lookups that legitimately miss.
var ErrNotFound = errors.New("not found")

// Store wraps a connection pool. Read paths take the pool directly; anything
// that mutates cash or positions goes through InTx and the package-level
// helpers that require a pgx.Tx.
type Store struct{ Pool *pgxpool.Pool }

// New opens the pool, registers numeric<->decimal handling, and applies the
// embedded migrations.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Teach pgx to scan NUMERIC straight into decimal.Decimal.
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		pgxdecimal.Register(c.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	s := &Store{Pool: pool}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.Pool.Close() }

// Migrate applies every embedded .sql file in lexical order. The files are
// idempotent (IF NOT EXISTS), which is enough for a schema this size; swap in a
// versioned migrator once the schema starts changing under live data.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		sql, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// InTx runs fn inside a serializable-enough transaction (READ COMMITTED plus
// row locks taken by the engine) and commits if fn returns nil.
func (s *Store) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

type Account struct {
	ID           int64
	Name         string
	StartingCash decimal.Decimal
	Cash         decimal.Decimal
	CreatedAt    time.Time
	// PasswordHash is nil for legacy accounts created before auth existed.
	PasswordHash *string
}

// Session is one login; the token is an opaque random string held by the
// client and looked up here on every request.
type Session struct {
	Token     string
	AccountID int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Position struct {
	ID            int64
	AccountID     int64
	TradingSymbol string
	Exchange      string
	Segment       string
	Quantity      int64
	AvgPrice      decimal.Decimal
	RealizedPnL   decimal.Decimal
}

type Order struct {
	ID              int64
	AccountID       int64
	OrderRef        string
	TradingSymbol   string
	Exchange        string
	Segment         string
	Product         string
	TransactionType string
	OrderType       string
	Quantity        int64
	LimitPrice      *decimal.Decimal
	// TriggerPrice arms an SL (stop-market) order: a long stop-loss SELL
	// becomes marketable once LTP <= trigger.
	TriggerPrice *decimal.Decimal
	// StopLoss/Target on a BUY describe the bracket to spawn after the fill.
	StopLoss *decimal.Decimal
	Target   *decimal.Decimal
	// TrailBy turns an SL into a trailing stop: the matcher ratchets
	// TriggerPrice up to (HighWater - TrailBy) as the price makes new highs.
	TrailBy   *decimal.Decimal
	HighWater *decimal.Decimal
	// OCOGroup ties sibling exit orders together: when one fills, the rest of
	// the group is cancelled.
	OCOGroup       *string
	Status         string
	FillPrice      *decimal.Decimal
	FilledQuantity int64
	Message        *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Trade struct {
	ID              int64
	AccountID       int64
	OrderRef        string
	TradingSymbol   string
	TransactionType string
	Quantity        int64
	Price           decimal.Decimal
	RealizedPnL     decimal.Decimal
	CreatedAt       time.Time
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

const accountCols = `id, name, starting_cash, cash, created_at, password_hash`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.StartingCash, &a.Cash, &a.CreatedAt, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// ErrNameTaken distinguishes "username exists" from infrastructure failures.
var ErrNameTaken = errors.New("username already taken")

// CreateUser registers a new account with its own starting equity.
func (s *Store) CreateUser(ctx context.Context, name, passwordHash string, startingCash decimal.Decimal) (Account, error) {
	const q = `
		INSERT INTO accounts (name, starting_cash, cash, password_hash)
		VALUES ($1, $2, $2, $3)
		ON CONFLICT (name) DO NOTHING
		RETURNING ` + accountCols
	acct, err := scanAccount(s.Pool.QueryRow(ctx, q, name, startingCash, passwordHash))
	if errors.Is(err, ErrNotFound) {
		return Account{}, ErrNameTaken
	}
	return acct, err
}

// GetAccountByName is the login lookup.
func (s *Store) GetAccountByName(ctx context.Context, name string) (Account, error) {
	const q = `SELECT ` + accountCols + ` FROM accounts WHERE name = $1`
	return scanAccount(s.Pool.QueryRow(ctx, q, name))
}

// SetStartingCash re-bases an account: new starting equity, positions and
// history wiped — the "change my equity to any number" operation.
func SetStartingCash(ctx context.Context, tx pgx.Tx, accountID int64, cash decimal.Decimal) error {
	if err := ResetAccount(ctx, tx, accountID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`UPDATE accounts SET starting_cash = $2, cash = $2 WHERE id = $1`, accountID, cash)
	return err
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

func (s *Store) CreateSession(ctx context.Context, token string, accountID int64, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO sessions (token, account_id, expires_at) VALUES ($1, $2, now() + $3::interval)`,
		token, accountID, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

// SessionAccount resolves a token to its account, if the session is alive.
// Columns are table-qualified: both tables carry created_at.
func (s *Store) SessionAccount(ctx context.Context, token string) (Account, error) {
	const q = `
		SELECT a.id, a.name, a.starting_cash, a.cash, a.created_at, a.password_hash
		FROM accounts a
		JOIN sessions s ON s.account_id = a.id
		WHERE s.token = $1 AND s.expires_at > now()`
	return scanAccount(s.Pool.QueryRow(ctx, q, token))
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}

// AccountIDsWithOpenOrders feeds the matcher: every account that has resting
// orders to try, not just one hard-wired default.
func (s *Store) AccountIDsWithOpenOrders(ctx context.Context) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT account_id FROM orders WHERE status = 'OPEN'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EnsureAccount returns the named paper account, creating it with the given
// starting cash the first time it is asked for.
func (s *Store) EnsureAccount(ctx context.Context, name string, startingCash decimal.Decimal) (Account, error) {
	const q = `
		INSERT INTO accounts (name, starting_cash, cash)
		VALUES ($1, $2, $2)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING ` + accountCols
	return scanAccount(s.Pool.QueryRow(ctx, q, name, startingCash))
}

// GetAccount reads the current account row (cash included) without locking.
func (s *Store) GetAccount(ctx context.Context, id int64) (Account, error) {
	const q = `SELECT ` + accountCols + ` FROM accounts WHERE id = $1`
	return scanAccount(s.Pool.QueryRow(ctx, q, id))
}

// LockAccount re-reads the account FOR UPDATE, serialising concurrent order
// flow against the same cash balance.
func LockAccount(ctx context.Context, tx pgx.Tx, id int64) (Account, error) {
	const q = `SELECT ` + accountCols + ` FROM accounts WHERE id = $1 FOR UPDATE`
	return scanAccount(tx.QueryRow(ctx, q, id))
}

func UpdateAccountCash(ctx context.Context, tx pgx.Tx, id int64, cash decimal.Decimal) error {
	_, err := tx.Exec(ctx, `UPDATE accounts SET cash = $2 WHERE id = $1`, id, cash)
	return err
}

// ---------------------------------------------------------------------------
// Positions
// ---------------------------------------------------------------------------

const positionCols = `id, account_id, trading_symbol, exchange, segment, quantity, avg_price, realized_pnl`

func scanPositions(rows pgx.Rows) ([]Position, error) {
	defer rows.Close()
	var out []Position
	for rows.Next() {
		var p Position
		if err := rows.Scan(&p.ID, &p.AccountID, &p.TradingSymbol, &p.Exchange,
			&p.Segment, &p.Quantity, &p.AvgPrice, &p.RealizedPnL); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListPositions(ctx context.Context, accountID int64) ([]Position, error) {
	const q = `SELECT ` + positionCols + ` FROM positions WHERE account_id = $1 ORDER BY trading_symbol`
	rows, err := s.Pool.Query(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	return scanPositions(rows)
}

// GetPositionForUpdate locks a single position row; ErrNotFound means the
// account holds nothing in that symbol yet.
func GetPositionForUpdate(ctx context.Context, tx pgx.Tx, accountID int64, symbol, segment string) (Position, error) {
	const q = `SELECT ` + positionCols + `
		FROM positions
		WHERE account_id = $1 AND trading_symbol = $2 AND segment = $3
		FOR UPDATE`
	var p Position
	err := tx.QueryRow(ctx, q, accountID, symbol, segment).Scan(&p.ID, &p.AccountID,
		&p.TradingSymbol, &p.Exchange, &p.Segment, &p.Quantity, &p.AvgPrice, &p.RealizedPnL)
	if errors.Is(err, pgx.ErrNoRows) {
		return Position{}, ErrNotFound
	}
	return p, err
}

// UpsertPosition writes the post-fill state of a holding.
func UpsertPosition(ctx context.Context, tx pgx.Tx, p Position) error {
	const q = `
		INSERT INTO positions (account_id, trading_symbol, exchange, segment, quantity, avg_price, realized_pnl)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (account_id, trading_symbol, segment) DO UPDATE
		SET quantity = EXCLUDED.quantity,
		    avg_price = EXCLUDED.avg_price,
		    realized_pnl = EXCLUDED.realized_pnl,
		    exchange = EXCLUDED.exchange`
	_, err := tx.Exec(ctx, q, p.AccountID, p.TradingSymbol, p.Exchange, p.Segment,
		p.Quantity, p.AvgPrice, p.RealizedPnL)
	return err
}

// ---------------------------------------------------------------------------
// Orders
// ---------------------------------------------------------------------------

const orderCols = `id, account_id, order_ref, trading_symbol, exchange, segment, product,
	transaction_type, order_type, quantity, limit_price, trigger_price, stop_loss,
	target, trail_by, high_water, oco_group, status, fill_price, filled_quantity,
	message, created_at, updated_at`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.AccountID, &o.OrderRef, &o.TradingSymbol, &o.Exchange,
		&o.Segment, &o.Product, &o.TransactionType, &o.OrderType, &o.Quantity,
		&o.LimitPrice, &o.TriggerPrice, &o.StopLoss, &o.Target, &o.TrailBy,
		&o.HighWater, &o.OCOGroup, &o.Status, &o.FillPrice, &o.FilledQuantity,
		&o.Message, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}

func scanOrders(rows pgx.Rows) ([]Order, error) {
	defer rows.Close()
	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func InsertOrder(ctx context.Context, tx pgx.Tx, o Order) (Order, error) {
	const q = `
		INSERT INTO orders (account_id, order_ref, trading_symbol, exchange, segment,
			product, transaction_type, order_type, quantity, limit_price, trigger_price,
			stop_loss, target, trail_by, high_water, oco_group, status, fill_price,
			filled_quantity, message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		RETURNING ` + orderCols
	return scanOrder(tx.QueryRow(ctx, q, o.AccountID, o.OrderRef, o.TradingSymbol,
		o.Exchange, o.Segment, o.Product, o.TransactionType, o.OrderType, o.Quantity,
		o.LimitPrice, o.TriggerPrice, o.StopLoss, o.Target, o.TrailBy, o.HighWater,
		o.OCOGroup, o.Status, o.FillPrice, o.FilledQuantity, o.Message))
}

func UpdateOrderOutcome(ctx context.Context, tx pgx.Tx, o Order) (Order, error) {
	const q = `
		UPDATE orders
		SET status = $2, fill_price = $3, filled_quantity = $4, message = $5, updated_at = now()
		WHERE id = $1
		RETURNING ` + orderCols
	return scanOrder(tx.QueryRow(ctx, q, o.ID, o.Status, o.FillPrice, o.FilledQuantity, o.Message))
}

func (s *Store) ListOrders(ctx context.Context, accountID int64, limit int) ([]Order, error) {
	const q = `SELECT ` + orderCols + `
		FROM orders WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`
	rows, err := s.Pool.Query(ctx, q, accountID, limit)
	if err != nil {
		return nil, err
	}
	return scanOrders(rows)
}

// ListOpenOrdersForUpdate locks the resting LIMIT orders so the matcher and an
// inbound cancel can't both act on the same row.
func ListOpenOrdersForUpdate(ctx context.Context, tx pgx.Tx, accountID int64) ([]Order, error) {
	const q = `SELECT ` + orderCols + `
		FROM orders WHERE account_id = $1 AND status = 'OPEN' ORDER BY created_at FOR UPDATE`
	rows, err := tx.Query(ctx, q, accountID)
	if err != nil {
		return nil, err
	}
	return scanOrders(rows)
}

func GetOrderForUpdate(ctx context.Context, tx pgx.Tx, accountID int64, orderRef string) (Order, error) {
	const q = `SELECT ` + orderCols + `
		FROM orders WHERE account_id = $1 AND order_ref = $2 FOR UPDATE`
	return scanOrder(tx.QueryRow(ctx, q, accountID, orderRef))
}

// UpdateOrderTrail persists a ratcheted trailing-stop level.
func UpdateOrderTrail(ctx context.Context, tx pgx.Tx, id int64, trigger, highWater decimal.Decimal) error {
	_, err := tx.Exec(ctx,
		`UPDATE orders SET trigger_price = $2, high_water = $3, updated_at = now() WHERE id = $1`,
		id, trigger, highWater)
	return err
}

// CancelOCOSiblings cancels every other OPEN order in the group. Returns the
// refs it cancelled so the matcher can skip them in the same pass.
func CancelOCOSiblings(ctx context.Context, tx pgx.Tx, accountID int64, group, exceptRef string) ([]string, error) {
	const q = `
		UPDATE orders
		SET status = 'CANCELLED', message = 'OCO sibling filled.', updated_at = now()
		WHERE account_id = $1 AND oco_group = $2 AND order_ref <> $3 AND status = 'OPEN'
		RETURNING order_ref`
	rows, err := tx.Query(ctx, q, accountID, group, exceptRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// ---------------------------------------------------------------------------
// Trades
// ---------------------------------------------------------------------------

const tradeCols = `id, account_id, order_ref, trading_symbol, transaction_type,
	quantity, price, realized_pnl, created_at`

func InsertTrade(ctx context.Context, tx pgx.Tx, t Trade) error {
	const q = `
		INSERT INTO trades (account_id, order_ref, trading_symbol, transaction_type, quantity, price, realized_pnl)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := tx.Exec(ctx, q, t.AccountID, t.OrderRef, t.TradingSymbol,
		t.TransactionType, t.Quantity, t.Price, t.RealizedPnL)
	return err
}

func (s *Store) ListTrades(ctx context.Context, accountID int64, limit int) ([]Trade, error) {
	const q = `SELECT ` + tradeCols + `
		FROM trades WHERE account_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`
	rows, err := s.Pool.Query(ctx, q, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Trade
	for rows.Next() {
		var t Trade
		if err := rows.Scan(&t.ID, &t.AccountID, &t.OrderRef, &t.TradingSymbol,
			&t.TransactionType, &t.Quantity, &t.Price, &t.RealizedPnL, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

// ResetAccount wipes all trading history and restores the starting balance.
func ResetAccount(ctx context.Context, tx pgx.Tx, accountID int64) error {
	for _, q := range []string{
		`DELETE FROM trades WHERE account_id = $1`,
		`DELETE FROM orders WHERE account_id = $1`,
		`DELETE FROM positions WHERE account_id = $1`,
		`UPDATE accounts SET cash = starting_cash WHERE id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, accountID); err != nil {
			return err
		}
	}
	return nil
}
