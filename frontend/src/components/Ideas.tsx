import { useEffect, useState } from 'react'

import { api, type Idea, type Recommendations } from '../lib/api'
import { inr, num, pct, timeOf } from '../lib/format'
import { useToast } from '../lib/toast'
import { ConfirmDialog } from './ConfirmDialog'

const FEATURE_LABEL: Record<string, string> = {
  momentum_60d: '3-month momentum',
  momentum_20d: '1-month momentum',
  trend_persistence: 'Trend persistence',
  proximity_to_high: 'Near 60d high',
  volume_ratio: 'Volume expansion',
  rsi_band: 'RSI health',
}

/**
 * Model-ranked positional ideas, each pre-sized to the risk brief (default:
 * lose ₹20–30k at the stop, make ₹30–50k at the target). Loads itself when
 * the tab opens; the backtest of the rule sits right above the picks.
 */
export function Ideas({
  active,
  onTraded,
  onSelectSymbol,
}: {
  active: boolean
  onTraded: () => void
  onSelectSymbol: (symbol: string) => void
}) {
  const toast = useToast()
  const [data, setData] = useState<Recommendations | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [pending, setPending] = useState<Idea | null>(null)
  const [busy, setBusy] = useState(false)

  async function runScan() {
    setLoading(true)
    setError(null)
    try {
      setData(await api.recommendations())
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (active && !data && !loading && !error) void runScan()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  async function confirmTrade() {
    const idea = pending
    if (!idea?.plan) return
    setBusy(true)
    try {
      const order = await api.placeOrder({
        trading_symbol: idea.symbol,
        transaction_type: 'BUY',
        order_type: 'MARKET',
        quantity: idea.plan.quantity,
        stop_loss: idea.plan.stop_loss,
        target: idea.plan.target,
      })
      toast.push(
        order.status === 'FILLED' ? 'success' : 'error',
        `${idea.symbol}: ${order.status}${order.status === 'FILLED' ? ` @ ${inr(order.fill_price ?? 0)} — bracket armed` : ''}`,
      )
      onTraded()
    } catch (err) {
      toast.push('error', (err as Error).message)
    } finally {
      setBusy(false)
      setPending(null)
    }
  }

  const bt = data?.backtest

  return (
    <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-3">
        <div>
          <span className="text-sm font-semibold">Positional ideas</span>
          <span className="ml-2 text-xs text-slate-500">
            momentum screen · 3-month features · sized for ₹20–30k risk / ₹30–50k target
          </span>
        </div>
        <div className="flex items-center gap-2 text-xs text-slate-500">
          {data ? <span>updated {timeOf(data.as_of)}</span> : null}
          <button
            type="button"
            onClick={runScan}
            disabled={loading}
            className="rounded-lg border border-slate-700 px-3 py-1.5 font-medium text-slate-300 transition-colors hover:bg-slate-800 disabled:opacity-60"
          >
            {loading ? 'Scanning…' : 'Refresh'}
          </button>
        </div>
      </div>

      {error ? (
        <div className="px-4 py-3 text-sm text-rose-300">
          {error}{' '}
          <button type="button" onClick={runScan} className="underline">
            retry
          </button>
        </div>
      ) : null}

      {!data && loading ? (
        <div className="px-4 py-10 text-center text-sm text-slate-500">
          <div className="mx-auto mb-3 h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-slate-200" />
          Scoring ~200 F&O stocks on a year of daily bars — a cold run takes a few seconds…
        </div>
      ) : null}

      {data ? (
        <>
          {/* The honesty box: what this rule actually did historically. */}
          {bt && bt.folds > 0 ? (
            <div className="border-b border-slate-800 bg-slate-950/40 px-4 py-2.5 text-xs text-slate-400">
              Backtest ({bt.folds} monthly folds, {bt.horizon_days}d holds): rank-IC{' '}
              <span className="tabular-nums text-slate-200">{bt.mean_ic.toFixed(2)}</span> · top-decile hit rate{' '}
              <span className="tabular-nums text-slate-200">{(bt.top_decile_hit_rate * 100).toFixed(0)}%</span> · top-decile
              avg <span className="tabular-nums text-slate-200">{(bt.top_decile_mean_return * 100).toFixed(1)}%</span> vs
              universe <span className="tabular-nums text-slate-200">{(bt.universe_mean_return * 100).toFixed(1)}%</span>
              <span className="ml-1 text-slate-600">— a tilt, not a promise.</span>
            </div>
          ) : null}

          <ul className="divide-y divide-slate-800/70">
            {data.picks.map((idea) => {
              const p = idea.plan
              const isOpen = expanded === idea.symbol
              return (
                <li key={idea.symbol} className="px-4 py-3">
                  <div className="flex flex-wrap items-center gap-3">
                    <button
                      type="button"
                      onClick={() => onSelectSymbol(idea.symbol)}
                      className="min-w-0 text-left hover:underline"
                      title="Open on the Trade tab"
                    >
                      <span className="font-semibold text-slate-100">
                        #{idea.rank} {idea.symbol}
                      </span>
                      <span className="ml-2 truncate text-xs text-slate-500">{idea.name}</span>
                    </button>

                    <span className="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] tabular-nums text-slate-300">
                      score {idea.score.toFixed(2)}
                    </span>

                    <div className="ml-auto flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => setExpanded(isOpen ? null : idea.symbol)}
                        className="rounded border border-slate-700 px-2 py-1 text-xs text-slate-400 hover:bg-slate-800"
                      >
                        {isOpen ? 'Hide why' : 'Why?'}
                      </button>
                      {p ? (
                        <button
                          type="button"
                          onClick={() => setPending(idea)}
                          className="rounded-lg bg-emerald-600 px-3 py-1 text-xs font-semibold text-white hover:bg-emerald-500"
                        >
                          Buy with bracket
                        </button>
                      ) : null}
                    </div>
                  </div>

                  {p ? (
                    <div className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-xs tabular-nums sm:grid-cols-4 lg:grid-cols-7">
                      <Cell label="Entry" value={inr(p.entry)} />
                      <Cell label="Qty" value={num(p.quantity)} />
                      <Cell label="Capital" value={inr(p.capital_required)} />
                      <Cell label="Stop" value={inr(p.stop_loss)} tone="down" />
                      <Cell label="Max loss" value={inr(p.loss_at_stop)} tone="down" />
                      <Cell label="Target" value={inr(p.target)} tone="up" />
                      <Cell label="Profit" value={inr(p.profit_at_target)} tone="up" />
                    </div>
                  ) : (
                    <p className="mt-2 text-xs text-amber-400">{idea.plan_note}</p>
                  )}
                  {p && idea.plan_note ? <p className="mt-1 text-xs text-amber-400">{idea.plan_note}</p> : null}

                  {isOpen ? (
                    <div className="mt-2 rounded-lg bg-slate-950/50 p-3">
                      <div className="grid gap-1 text-xs sm:grid-cols-2">
                        {Object.entries(idea.z_contributions)
                          .sort(([, a], [, b]) => b - a)
                          .map(([key, value]) => (
                            <div key={key} className="flex items-center justify-between gap-2">
                              <span className="text-slate-500">{FEATURE_LABEL[key] ?? key}</span>
                              <span
                                className={`tabular-nums ${value >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}
                              >
                                {value >= 0 ? '+' : ''}
                                {value.toFixed(2)}
                              </span>
                            </div>
                          ))}
                      </div>
                      <p className="mt-2 text-xs text-slate-500">
                        3-mo {pct(idea.features.momentum_60d * 100)} · 1-mo {pct(idea.features.momentum_20d * 100)} · RSI{' '}
                        {num(idea.features.rsi_14)} · daily range ±{num(idea.features.atr_pct * 100)}% · stop = 2×ATR,
                        target = 1.6× the stop distance
                      </p>
                    </div>
                  ) : null}
                </li>
              )
            })}
          </ul>

          <p className="border-t border-slate-800 px-4 py-2 text-[11px] text-slate-600">
            {data.caveats.join(' ')} Scored {data.scored} of {data.universe_size} F&O stocks
            {data.skipped > 0 ? ` (${data.skipped} screened out)` : ''} · prices via {data.price_source}.
          </p>
        </>
      ) : null}

      <ConfirmDialog
        open={pending !== null}
        title={`Buy ${pending?.symbol ?? ''} with bracket`}
        rows={
          pending?.plan
            ? [
                { label: 'Quantity', value: num(pending.plan.quantity) },
                { label: 'Entry (market)', value: `~${inr(pending.plan.entry)}` },
                { label: 'Capital', value: inr(pending.plan.capital_required) },
                { label: 'Stop-loss', value: inr(pending.plan.stop_loss), tone: 'down' },
                { label: 'Max loss', value: inr(pending.plan.loss_at_stop), tone: 'down' },
                { label: 'Target', value: inr(pending.plan.target), tone: 'up' },
                { label: 'Profit at target', value: inr(pending.plan.profit_at_target), tone: 'up' },
              ]
            : []
        }
        note="Stop and target rest as an OCO pair after the fill — whichever hits first cancels the other."
        confirmLabel="Place order"
        busy={busy}
        onConfirm={confirmTrade}
        onCancel={() => setPending(null)}
      />
    </div>
  )
}

function Cell({ label, value, tone }: { label: string; value: string; tone?: 'up' | 'down' }) {
  const cls = tone === 'up' ? 'text-emerald-400' : tone === 'down' ? 'text-rose-400' : 'text-slate-200'
  return (
    <div>
      <span className="block text-slate-500">{label}</span>
      <span className={`block ${cls}`}>{value}</span>
    </div>
  )
}
