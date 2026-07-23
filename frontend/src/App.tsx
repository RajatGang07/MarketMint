import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { Blotter } from './components/Blotter'
import { ConfirmDialog } from './components/ConfirmDialog'
import { Ideas } from './components/Ideas'
import { IntradayScanner } from './components/IntradayScanner'
import { OrderTicket } from './components/OrderTicket'
import { PriceChart } from './components/PriceChart'
import { QuoteHeader } from './components/QuoteHeader'
import { SignalsBoard } from './components/SignalsBoard'
import { StatCard } from './components/StatCard'
import { SymbolSearch } from './components/SymbolSearch'
import { Watchlist } from './components/Watchlist'
import { api, type ChartRange, type Quote } from './lib/api'
import { inr, pct, signedInr } from './lib/format'
import { ToastProvider, useToast } from './lib/toast'
import { usePoll } from './lib/usePoll'

const DEFAULT_SYMBOLS = ['RELIANCE', 'TCS', 'INFY', 'HDFCBANK', 'SBIN', 'ICICIBANK']
const WATCHLIST_KEY = 'paper-trading.watchlist'
const TAB_KEY = 'paper-trading.tab'

const QUOTE_INTERVAL = 5_000
const ACCOUNT_INTERVAL = 5_000
const CHART_INTERVAL = 30_000
const HEALTH_INTERVAL = 20_000

type Tab = 'trade' | 'ideas'
type IdeasTab = 'signals' | 'intraday' | 'positional'

function loadWatchlist(): string[] {
  try {
    const raw = localStorage.getItem(WATCHLIST_KEY)
    const parsed = raw ? (JSON.parse(raw) as unknown) : null
    if (Array.isArray(parsed) && parsed.every((s) => typeof s === 'string') && parsed.length > 0) {
      return parsed as string[]
    }
  } catch {
    // Corrupt or unavailable storage is not worth failing the app over.
  }
  return DEFAULT_SYMBOLS
}

/** NSE cash session, computed on the exchange clock (IST). */
function marketIsOpen(): boolean {
  const ist = new Date(Date.now() + (330 + new Date().getTimezoneOffset()) * 60_000)
  const day = ist.getDay()
  if (day === 0 || day === 6) return false
  const mins = ist.getHours() * 60 + ist.getMinutes()
  return mins >= 9 * 60 + 15 && mins < 15 * 60 + 30
}

export default function App() {
  return (
    <ToastProvider>
      <Dashboard />
    </ToastProvider>
  )
}

function Dashboard() {
  const toast = useToast()
  const [tab, setTab] = useState<Tab>(() => (localStorage.getItem(TAB_KEY) === 'ideas' ? 'ideas' : 'trade'))
  const [ideasTab, setIdeasTab] = useState<IdeasTab>('signals')
  const [symbols, setSymbols] = useState<string[]>(loadWatchlist)
  const [selected, setSelected] = useState<string>(() => loadWatchlist()[0])
  const [range, setRange] = useState<ChartRange>('1d')
  const [confirmReset, setConfirmReset] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [marketOpen, setMarketOpen] = useState(marketIsOpen)
  const searchRef = useRef<HTMLInputElement>(null)

  const health = usePoll(api.health, HEALTH_INTERVAL)
  const portfolio = usePoll(api.portfolio, ACCOUNT_INTERVAL)
  const orders = usePoll(api.orders, ACCOUNT_INTERVAL)
  const trades = usePoll(api.trades, ACCOUNT_INTERVAL)

  const quoteSymbols = useMemo(() => Array.from(new Set([selected, ...symbols])), [selected, symbols])
  const quotes = usePoll(
    useCallback(() => api.quotes(quoteSymbols), [quoteSymbols]),
    QUOTE_INTERVAL,
    [quoteSymbols.join(',')],
  )
  const candles = usePoll(
    useCallback(() => api.candles(selected, range), [selected, range]),
    CHART_INTERVAL,
    [selected, range],
  )

  // Keep the market chip honest without a reload.
  useEffect(() => {
    const id = window.setInterval(() => setMarketOpen(marketIsOpen()), 30_000)
    return () => window.clearInterval(id)
  }, [])

  // "/" focuses search from anywhere.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement
      const typing = target.tagName === 'INPUT' || target.tagName === 'SELECT' || target.tagName === 'TEXTAREA'
      if (e.key === '/' && !typing) {
        e.preventDefault()
        setTab('trade')
        window.setTimeout(() => searchRef.current?.focus(), 0)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const quoteBySymbol = useMemo(() => {
    const map: Record<string, Quote> = {}
    for (const q of quotes.data ?? []) map[q.symbol] = q
    return map
  }, [quotes.data])

  const selectedQuote = quoteBySymbol[selected]
  const p = portfolio.data

  const heldQuantity = useMemo(
    () => p?.positions.find((pos) => pos.trading_symbol === selected)?.quantity ?? 0,
    [p, selected],
  )

  const refreshAccount = useCallback(() => {
    portfolio.refresh()
    orders.refresh()
    trades.refresh()
  }, [portfolio, orders, trades])

  function switchTab(next: Tab) {
    setTab(next)
    localStorage.setItem(TAB_KEY, next)
  }

  function persist(next: string[]) {
    setSymbols(next)
    localStorage.setItem(WATCHLIST_KEY, JSON.stringify(next))
  }

  /** Adding from anywhere (search, signals, ideas) selects it and shows the chart. */
  function addSymbol(symbol: string) {
    const sym = symbol.trim().toUpperCase()
    if (!sym) return
    if (!symbols.includes(sym)) persist([...symbols, sym])
    setSelected(sym)
    switchTab('trade')
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  function removeSymbol(symbol: string) {
    const next = symbols.filter((s) => s !== symbol)
    persist(next)
    if (selected === symbol && next.length > 0) setSelected(next[0])
  }

  async function cancelOrder(orderRef: string) {
    try {
      await api.cancelOrder(orderRef)
      toast.push('info', 'Order cancelled')
    } catch (err) {
      toast.push('error', (err as Error).message)
    } finally {
      refreshAccount()
    }
  }

  async function doReset() {
    setResetting(true)
    try {
      await api.reset()
      toast.push('success', `Account reset — ${inr(p?.starting_cash ?? 1_000_000)} restored`)
      refreshAccount()
    } catch (err) {
      toast.push('error', (err as Error).message)
    } finally {
      setResetting(false)
      setConfirmReset(false)
    }
  }

  const live = health.data?.market_data_mode === 'live'
  const connectionError = portfolio.error ?? health.error

  const tabBtn = (id: Tab, label: string) => (
    <button
      type="button"
      onClick={() => switchTab(id)}
      aria-current={tab === id ? 'page' : undefined}
      className={`rounded-lg px-4 py-1.5 text-sm font-medium transition-colors ${
        tab === id ? 'bg-slate-100 text-slate-900' : 'text-slate-300 hover:bg-slate-800'
      }`}
    >
      {label}
    </button>
  )

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-10 border-b border-slate-800 bg-slate-950/90 backdrop-blur">
        <div className="mx-auto flex max-w-[92rem] flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
          <h1 className="text-lg font-semibold">MarketMint</h1>

          <nav className="flex gap-1 rounded-xl border border-slate-800 bg-slate-900/60 p-1">
            {tabBtn('trade', 'Trade')}
            {tabBtn('ideas', 'Ideas & Signals')}
          </nav>

          <div className="flex items-center gap-2 text-xs">
            <span
              className={`rounded-full px-2.5 py-1 font-medium ${
                marketOpen ? 'bg-emerald-500/15 text-emerald-400' : 'bg-slate-700/40 text-slate-400'
              }`}
            >
              {marketOpen ? '● Market open' : '○ Market closed'}
            </span>
            <span
              title={health.data?.market_data_note ?? 'Price feed status'}
              className={`rounded-full px-2.5 py-1 font-medium ${
                live ? 'bg-emerald-500/15 text-emerald-400' : 'bg-amber-500/15 text-amber-400'
              }`}
            >
              {live ? `Live · ${health.data?.market_data_source ?? ''}` : 'Simulated prices'}
            </span>
          </div>

          <div className="ml-auto flex items-center gap-4">
            {p ? (
              <div className="hidden text-right sm:block">
                <div className="text-sm font-semibold tabular-nums text-slate-100">{inr(p.equity)}</div>
                <div className={`text-xs tabular-nums ${p.total_pnl >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                  {signedInr(p.total_pnl)} ({pct(p.total_pnl_pct)})
                </div>
              </div>
            ) : null}
            <button
              type="button"
              onClick={() => setConfirmReset(true)}
              className="rounded-lg border border-slate-700 px-3 py-1.5 text-xs font-medium text-slate-300 transition-colors hover:bg-slate-800"
            >
              Reset
            </button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-[92rem] space-y-4 px-4 py-5">
        {connectionError ? (
          <div className="rounded-xl border border-rose-900/60 bg-rose-950/40 px-4 py-3 text-sm text-rose-300">
            Cannot reach the API — {connectionError}. Is the Go backend running on port 8000?
          </div>
        ) : null}

        {tab === 'trade' ? (
          <>
            <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <StatCard
                label="Equity"
                value={p ? inr(p.equity) : '—'}
                sub={p ? `Started at ${inr(p.starting_cash)}` : undefined}
              />
              <StatCard
                label="Cash available"
                value={p ? inr(p.cash) : '—'}
                sub={p ? `${inr(p.invested)} invested` : undefined}
              />
              <StatCard
                label="Unrealised P&L"
                value={p ? signedInr(p.unrealized_pnl) : '—'}
                tone={p ? (p.unrealized_pnl >= 0 ? 'up' : 'down') : 'neutral'}
                sub={p ? `Realised ${signedInr(p.realized_pnl)}` : undefined}
              />
              <StatCard
                label="Total P&L"
                value={p ? signedInr(p.total_pnl) : '—'}
                tone={p ? (p.total_pnl >= 0 ? 'up' : 'down') : 'neutral'}
                sub={p ? pct(p.total_pnl_pct) : undefined}
              />
            </section>

            <section className="grid gap-4 xl:grid-cols-[19rem_minmax(0,1fr)_19rem]">
              <div className="space-y-3">
                <SymbolSearch inputRef={searchRef} onPick={addSymbol} />
                <p className="px-1 text-[11px] text-slate-600">
                  Tip: press <kbd className="rounded bg-slate-800 px-1">/</kbd> to search · click any symbol
                  anywhere to open it here
                </p>
                <Watchlist
                  symbols={symbols}
                  quotes={quoteBySymbol}
                  selected={selected}
                  onSelect={setSelected}
                  onRemove={removeSymbol}
                />
              </div>

              <div className="space-y-4">
                <QuoteHeader symbol={selected} quote={selectedQuote} />
                <PriceChart
                  symbol={selected}
                  candles={candles.data?.candles ?? []}
                  previousClose={selectedQuote?.close}
                  range={range}
                  onRangeChange={setRange}
                  loading={!candles.data && !candles.error}
                  error={candles.error}
                />
              </div>

              <div>
                <OrderTicket
                  symbol={selected}
                  ltp={selectedQuote?.ok ? selectedQuote.last_price : undefined}
                  cash={p?.cash}
                  heldQuantity={heldQuantity}
                  onPlaced={refreshAccount}
                />
              </div>
            </section>

            <Blotter
              positions={p?.positions ?? []}
              orders={orders.data ?? []}
              trades={trades.data ?? []}
              onCancel={cancelOrder}
              onSelectSymbol={(s) => {
                setSelected(s)
                window.scrollTo({ top: 0, behavior: 'smooth' })
              }}
            />
          </>
        ) : (
          <>
            <nav className="flex flex-wrap gap-1 rounded-xl border border-slate-800 bg-slate-900/60 p-1">
              {(
                [
                  ['signals', 'Signals board', 'one verdict per stock'],
                  ['intraday', 'Intraday scanner', 'breakouts + mechanical exits'],
                  ['positional', 'Positional ideas', '1–2 month momentum picks'],
                ] as Array<[IdeasTab, string, string]>
              ).map(([id, label, hint]) => (
                <button
                  key={id}
                  type="button"
                  onClick={() => setIdeasTab(id)}
                  className={`rounded-lg px-3 py-2 text-left text-sm transition-colors ${
                    ideasTab === id ? 'bg-slate-800 text-slate-100' : 'text-slate-400 hover:bg-slate-800/50'
                  }`}
                >
                  <span className="block font-medium">{label}</span>
                  <span className="block text-[10px] text-slate-500">{hint}</span>
                </button>
              ))}
            </nav>

            <div className={ideasTab === 'signals' ? '' : 'hidden'}>
              <SignalsBoard
                active={tab === 'ideas' && ideasTab === 'signals'}
                onTraded={refreshAccount}
                onSelectSymbol={addSymbol}
              />
            </div>
            <div className={ideasTab === 'intraday' ? '' : 'hidden'}>
              <IntradayScanner
                active={tab === 'ideas' && ideasTab === 'intraday'}
                onTraded={refreshAccount}
                onSelectSymbol={addSymbol}
              />
            </div>
            <div className={ideasTab === 'positional' ? '' : 'hidden'}>
              <Ideas
                active={tab === 'ideas' && ideasTab === 'positional'}
                onTraded={refreshAccount}
                onSelectSymbol={addSymbol}
              />
            </div>
          </>
        )}
      </main>

      <ConfirmDialog
        open={confirmReset}
        danger
        title="Reset the paper account?"
        rows={
          p
            ? [
                { label: 'Current equity', value: inr(p.equity) },
                { label: 'Open positions', value: String(p.positions.length) },
                { label: 'Restores to', value: inr(p.starting_cash) },
              ]
            : []
        }
        note="All positions, orders and trade history are erased. This cannot be undone."
        confirmLabel="Reset everything"
        busy={resetting}
        onConfirm={doReset}
        onCancel={() => setConfirmReset(false)}
      />
    </div>
  )
}
