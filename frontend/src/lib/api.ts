// In dev the Vite server proxies nothing, so default to the local Go API;
// in a production build default to same-origin (the Go binary serves the UI).
// VITE_API_BASE overrides both (e.g. a Vercel frontend pointing at Render).
const BASE = import.meta.env.VITE_API_BASE ?? (import.meta.env.DEV ? 'http://localhost:8000' : '')

const TOKEN_KEY = 'paper-trading.session'

export const session = {
  token: (): string | null => localStorage.getItem(TOKEN_KEY),
  set: (token: string) => localStorage.setItem(TOKEN_KEY, token),
  clear: () => localStorage.removeItem(TOKEN_KEY),
}

/** Fired when the API answers 401 — the app returns to the sign-in screen. */
export const AUTH_REQUIRED_EVENT = 'marketmint:auth-required'

export interface Session {
  token: string
  username: string
  starting_cash: number
}

export interface Me {
  username: string
  starting_cash: number
  cash: number
}

export interface ProviderStatus {
  name: string
  healthy: boolean
  reason?: string
  active: boolean
}

export interface Health {
  status: string
  /** "live" when a real feed is serving prices, "mock" when simulated. */
  market_data_mode: string
  /** Which provider in the chain is actually answering. */
  market_data_source?: string
  market_data_providers?: ProviderStatus[]
  /** Why the preferred providers aren't being used. */
  market_data_note?: string
  instruments_loaded?: number
}

export interface Ltp {
  symbol: string
  exchange: string
  ltp: number
}

export interface Quote {
  symbol: string
  name?: string
  exchange: string
  last_price: number
  open: number
  high: number
  low: number
  /** Previous session's close — what day change is measured against. */
  close: number
  volume: number
  change: number
  change_pct: number
  ok: boolean
  error?: string
}

export interface Candle {
  time: string
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface CandleSeries {
  symbol: string
  range: string
  interval_minutes: number
  candles: Candle[]
}

export interface Instrument {
  trading_symbol: string
  name: string
  exchange: string
  segment: string
  series: string
  isin: string
  lot_size: number
  tick_size: string
  is_intraday: boolean
}

export interface Order {
  id: number
  order_ref: string
  trading_symbol: string
  exchange: string
  segment: string
  product: string
  transaction_type: string
  order_type: string
  quantity: number
  limit_price: number | null
  trigger_price: number | null
  stop_loss: number | null
  target: number | null
  oco_group: string | null
  status: string
  fill_price: number | null
  filled_quantity: number
  message: string | null
  created_at: string
}

export interface Trade {
  id: number
  order_ref: string
  trading_symbol: string
  transaction_type: string
  quantity: number
  price: number
  realized_pnl: number
  created_at: string
}

export interface Position {
  trading_symbol: string
  exchange: string
  segment: string
  quantity: number
  avg_price: number
  realized_pnl: number
  ltp: number
  market_value: number
  unrealized_pnl: number
}

export interface Portfolio {
  account_name: string
  starting_cash: number
  cash: number
  invested: number
  market_value: number
  equity: number
  realized_pnl: number
  unrealized_pnl: number
  total_pnl: number
  total_pnl_pct: number
  positions: Position[]
}

export interface OrderRequest {
  trading_symbol: string
  exchange?: string
  segment?: string
  product?: string
  transaction_type: string
  order_type: string
  quantity: number
  limit_price?: number | null
  trigger_price?: number | null
  /** With target, creates an OCO bracket once the buy fills. */
  stop_loss?: number | null
  target?: number | null
  /** Makes the bracket stop trail the high by this amount. */
  trail_by?: number | null
}

export interface TradePlan {
  entry: number
  stop_loss: number
  target: number
  quantity: number
  capital_required: number
  loss_at_stop: number
  profit_at_target: number
  risk_reward: number
  capital_capped: boolean
}

export interface IdeaFeatures {
  momentum_60d: number
  momentum_20d: number
  trend_persistence: number
  proximity_to_high: number
  volume_ratio: number
  rsi_14: number
  atr_14: number
  atr_pct: number
  turnover_20d: number
  last_close: number
}

export interface Idea {
  symbol: string
  name: string
  score: number
  rank: number
  features: IdeaFeatures
  z_contributions: Record<string, number>
  plan?: TradePlan
  plan_note?: string
}

export interface BacktestSummary {
  folds: number
  mean_ic: number
  top_decile_hit_rate: number
  top_decile_mean_return: number
  universe_mean_return: number
  horizon_days: number
  note: string
}

export interface Recommendations {
  as_of: string
  universe_size: number
  scored: number
  skipped: number
  picks: Idea[]
  others: Idea[]
  risk_bands: { loss_min: number; loss_max: number; profit_min: number; profit_max: number }
  backtest: BacktestSummary
  price_source: string
  caveats: string[]
}

export interface IntradayStats {
  trades: number
  wins: number
  win_rate: number
  avg_r: number
  avg_win_r: number
  avg_loss_r: number
  profit_factor: number
  best_r: number
  worst_r: number
}

export interface IntradayPick {
  symbol: string
  name: string
  rank: number
  status: string
  entry_time: string
  entry: number
  stop: number
  target: number
  trail_by: number
  risk_per_share: number
  or_high: number
  or_low: number
  vwap: number
  rvol: number
  last_price: number
  exit_time?: string
  exit?: number
  result_r?: number
  quantity?: number
  capital_required?: number
  max_loss?: number
  profit_at_target?: number
  capital_capped?: boolean
  history: IntradayStats
}

export interface IntradayResult {
  as_of: string
  session_date: string
  session_open: boolean
  session_note: string
  universe_size: number
  with_data: number
  triggered_today: number
  risk_per_trade: number
  picks: IntradayPick[]
  backtest: IntradayStats
  backtest_sessions: number
  price_source: string
  caveats: string[]
  rule: string
}

export interface SignalRow {
  action: string
  symbol: string
  name?: string
  last_price: number
  change_pct: number
  rank?: number
  score?: number
  reasons: string[]
  plan?: TradePlan
  held_quantity?: number
  avg_price?: number
  unrealized_pnl?: number
  unrealized_pct?: number
  exits_armed?: boolean
}

export interface SignalsBoard {
  as_of: string
  rows: SignalRow[]
  counts: Record<string, number>
  universe_size: number
  price_source: string
  session_open: boolean
  caveats: string[]
}

export type ChartRange = '1d' | '5d' | '1mo' | '3mo' | '1y'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  const token = session.token()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const res = await fetch(`${BASE}${path}`, { ...init, headers })
  if (res.status === 401) {
    session.clear()
    window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT))
  }
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    const detail = body && typeof body === 'object' ? (body as { detail?: string }).detail : null
    throw new Error(detail ?? `Request failed (${res.status})`)
  }
  return (await res.json()) as T
}

const list = (symbols: string[]) => encodeURIComponent(symbols.join(','))

export const api = {
  health: () => req<Health>('/health'),

  signup: (username: string, password: string, startingCash?: number) =>
    req<Session>('/auth/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password, starting_cash: startingCash ?? null }),
    }),
  login: (username: string, password: string) =>
    req<Session>('/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    }),
  logout: () => req<{ status: string }>('/auth/logout', { method: 'POST' }),
  me: () => req<Me>('/auth/me'),
  setEquity: (startingCash: number) =>
    req<Portfolio>('/portfolio/equity', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ starting_cash: startingCash }),
    }),

  ltp: (symbols: string[]) => req<Ltp[]>(`/market/ltp?symbols=${list(symbols)}`),
  quote: (symbol: string) => req<Quote>(`/market/quote?symbol=${encodeURIComponent(symbol)}`),
  quotes: (symbols: string[]) => req<Quote[]>(`/market/quotes?symbols=${list(symbols)}`),
  candles: (symbol: string, range: ChartRange) =>
    req<CandleSeries>(`/market/candles?symbol=${encodeURIComponent(symbol)}&range=${range}`),

  searchInstruments: (query: string, limit = 12) =>
    req<Instrument[]>(`/instruments/search?q=${encodeURIComponent(query)}&limit=${limit}`),

  portfolio: () => req<Portfolio>('/portfolio'),
  orders: () => req<Order[]>('/orders'),
  trades: () => req<Trade[]>('/trades'),

  recommendations: () => req<Recommendations>('/analytics/recommendations'),
  intraday: (risk?: number) =>
    req<IntradayResult>(`/analytics/intraday${risk ? `?risk=${risk}` : ''}`),
  signals: () => req<SignalsBoard>('/analytics/signals'),

  placeOrder: (body: OrderRequest) =>
    req<Order>('/orders', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  cancelOrder: (orderRef: string) =>
    req<Order>(`/orders/${encodeURIComponent(orderRef)}/cancel`, { method: 'POST' }),
  reset: () => req<Portfolio>('/portfolio/reset', { method: 'POST' }),
}
