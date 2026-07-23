import { useEffect, useState } from 'react'

import { api, type SignalRow, type SignalsBoard as Board } from '../lib/api'
import { inr, num, pct, signedInr, timeOf, toneClass } from '../lib/format'
import { useToast } from '../lib/toast'
import { ConfirmDialog } from './ConfirmDialog'

const ACTION_STYLE: Record<string, string> = {
  BUY: 'bg-emerald-500/15 text-emerald-400',
  SELL: 'bg-rose-500/15 text-rose-400',
  WATCH: 'bg-amber-500/15 text-amber-400',
  HOLD: 'bg-slate-600/30 text-slate-300',
}

type Pending = { mode: 'buy' | 'sell'; row: SignalRow } | null

/**
 * The verdict table: one row per stock worth acting on. Loads itself the
 * first time it becomes visible; refresh is one click and shows its age.
 */
export function SignalsBoard({
  active,
  onTraded,
  onSelectSymbol,
}: {
  active: boolean
  onTraded: () => void
  onSelectSymbol: (symbol: string) => void
}) {
  const toast = useToast()
  const [board, setBoard] = useState<Board | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<Pending>(null)
  const [busy, setBusy] = useState(false)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      setBoard(await api.signals())
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  // Auto-build on first visit — no dead "click to start" wall.
  useEffect(() => {
    if (active && !board && !loading && !error) void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  async function confirmPending() {
    if (!pending) return
    const { mode, row } = pending
    setBusy(true)
    try {
      if (mode === 'buy' && row.plan) {
        const order = await api.placeOrder({
          trading_symbol: row.symbol,
          transaction_type: 'BUY',
          order_type: 'MARKET',
          quantity: row.plan.quantity,
          stop_loss: row.plan.stop_loss,
          target: row.plan.target,
        })
        toast.push(
          order.status === 'FILLED' ? 'success' : 'error',
          `${row.symbol}: ${order.status}${order.status === 'FILLED' ? ` @ ${inr(order.fill_price ?? 0)} — bracket armed` : ''}`,
        )
      } else if (mode === 'sell' && row.held_quantity) {
        // Cancel resting exits first so the bracket can't double-sell.
        const open = (await api.orders()).filter(
          (o) => o.trading_symbol === row.symbol && o.status === 'OPEN' && o.transaction_type === 'SELL',
        )
        for (const o of open) await api.cancelOrder(o.order_ref).catch(() => undefined)
        const order = await api.placeOrder({
          trading_symbol: row.symbol,
          transaction_type: 'SELL',
          order_type: 'MARKET',
          quantity: row.held_quantity,
        })
        toast.push(
          order.status === 'FILLED' ? 'success' : 'error',
          `${row.symbol}: sold ${order.filled_quantity} — ${order.status}`,
        )
      }
      onTraded()
      void load()
    } catch (err) {
      toast.push('error', (err as Error).message)
    } finally {
      setBusy(false)
      setPending(null)
    }
  }

  const counts = board?.counts ?? {}

  return (
    <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-3">
        <div className="flex items-center gap-3">
          <span className="text-sm font-semibold">Signals</span>
          {board ? (
            <span className="flex gap-1.5 text-xs">
              {(['BUY', 'SELL', 'WATCH', 'HOLD'] as const).map((a) =>
                counts[a] ? (
                  <span key={a} className={`rounded px-1.5 py-0.5 ${ACTION_STYLE[a]}`}>
                    {a} {counts[a]}
                  </span>
                ) : null,
              )}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-2 text-xs text-slate-500">
          {board ? <span>updated {timeOf(board.as_of)}</span> : null}
          <button
            type="button"
            onClick={load}
            disabled={loading}
            className="rounded-lg border border-slate-700 px-3 py-1.5 font-medium text-slate-300 transition-colors hover:bg-slate-800 disabled:opacity-60"
          >
            {loading ? 'Building…' : 'Refresh'}
          </button>
        </div>
      </div>

      {error ? (
        <div className="px-4 py-3 text-sm text-rose-300">
          {error}{' '}
          <button type="button" onClick={load} className="underline">
            retry
          </button>
        </div>
      ) : null}

      {!board && loading ? (
        <div className="px-4 py-10 text-center text-sm text-slate-500">
          <div className="mx-auto mb-3 h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-slate-200" />
          Ranking 200+ stocks and checking your holdings — first build takes a few seconds…
        </div>
      ) : null}

      {board ? (
        <>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs uppercase tracking-wide text-slate-500">
                  <th className="px-4 py-2 font-medium">Action</th>
                  <th className="px-4 py-2 font-medium">Stock</th>
                  <th className="px-4 py-2 text-right font-medium">LTP</th>
                  <th className="px-4 py-2 text-right font-medium">Day</th>
                  <th className="px-4 py-2 font-medium">Why</th>
                  <th className="px-4 py-2 text-right font-medium">Levels / Position</th>
                  <th className="px-4 py-2 text-right font-medium"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/70">
                {board.rows.map((row) => (
                  <tr key={`${row.action}-${row.symbol}`} className="align-top tabular-nums">
                    <td className="px-4 py-2.5">
                      <span
                        className={`inline-block rounded px-2 py-0.5 text-xs font-semibold ${ACTION_STYLE[row.action] ?? ''}`}
                      >
                        {row.action}
                      </span>
                    </td>
                    <td className="px-4 py-2.5">
                      <button
                        type="button"
                        onClick={() => onSelectSymbol(row.symbol)}
                        className="text-left hover:underline"
                        title="Open on the Trade tab"
                      >
                        <span className="font-medium text-slate-100">{row.symbol}</span>
                        {row.rank ? <span className="ml-1.5 text-xs text-slate-500">#{row.rank}</span> : null}
                        {row.name ? <span className="block text-xs text-slate-500">{row.name}</span> : null}
                      </button>
                    </td>
                    <td className="px-4 py-2.5 text-right text-slate-300">{num(row.last_price)}</td>
                    <td className={`px-4 py-2.5 text-right text-xs ${toneClass(row.change_pct)}`}>
                      {pct(row.change_pct)}
                    </td>
                    <td className="max-w-md px-4 py-2.5">
                      <ul className="space-y-0.5 text-xs text-slate-400">
                        {row.reasons.map((reason) => (
                          <li key={reason}>• {reason}</li>
                        ))}
                      </ul>
                    </td>
                    <td className="px-4 py-2.5 text-right text-xs text-slate-300">
                      {row.plan ? (
                        <>
                          <span className="block">
                            {num(row.plan.quantity)} qty · {inr(row.plan.capital_required)}
                          </span>
                          <span className="block text-rose-400">
                            stop {num(row.plan.stop_loss)} (−{inr(row.plan.loss_at_stop)})
                          </span>
                          <span className="block text-emerald-400">
                            target {num(row.plan.target)} (+{inr(row.plan.profit_at_target)})
                          </span>
                        </>
                      ) : row.held_quantity ? (
                        <>
                          <span className="block">
                            {num(row.held_quantity)} qty @ {num(row.avg_price ?? 0)}
                          </span>
                          <span className={`block ${toneClass(row.unrealized_pnl ?? 0)}`}>
                            {signedInr(row.unrealized_pnl ?? 0)} ({pct(row.unrealized_pct ?? 0)})
                          </span>
                          <span className="block text-slate-500">
                            {row.exits_armed ? '✓ exits armed' : '⚠ no exits resting'}
                          </span>
                        </>
                      ) : (
                        '—'
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      {row.action === 'BUY' && row.plan ? (
                        <button
                          type="button"
                          onClick={() => setPending({ mode: 'buy', row })}
                          className="rounded-lg bg-emerald-600 px-3 py-1 text-xs font-semibold text-white hover:bg-emerald-500"
                        >
                          Buy
                        </button>
                      ) : null}
                      {row.action === 'SELL' && row.held_quantity ? (
                        <button
                          type="button"
                          onClick={() => setPending({ mode: 'sell', row })}
                          className="rounded-lg bg-rose-600 px-3 py-1 text-xs font-semibold text-white hover:bg-rose-500"
                        >
                          Sell all
                        </button>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <p className="border-t border-slate-800 px-4 py-2 text-[11px] text-slate-600">
            {board.caveats.join(' ')} Universe {board.universe_size} · prices via {board.price_source}.
          </p>
        </>
      ) : null}

      <ConfirmDialog
        open={pending?.mode === 'buy'}
        title={`Buy ${pending?.row.symbol ?? ''} with bracket`}
        rows={
          pending?.row.plan
            ? [
                { label: 'Quantity', value: num(pending.row.plan.quantity) },
                { label: 'Entry (market)', value: `~${inr(pending.row.plan.entry)}` },
                { label: 'Capital', value: inr(pending.row.plan.capital_required) },
                { label: 'Stop-loss', value: inr(pending.row.plan.stop_loss), tone: 'down' },
                { label: 'Max loss', value: inr(pending.row.plan.loss_at_stop), tone: 'down' },
                { label: 'Target', value: inr(pending.row.plan.target), tone: 'up' },
                { label: 'Profit at target', value: inr(pending.row.plan.profit_at_target), tone: 'up' },
              ]
            : []
        }
        note="Stop and target rest as an OCO pair after the fill — whichever hits first cancels the other."
        confirmLabel="Place order"
        busy={busy}
        onConfirm={confirmPending}
        onCancel={() => setPending(null)}
      />
      <ConfirmDialog
        open={pending?.mode === 'sell'}
        danger
        title={`Sell all ${pending?.row.symbol ?? ''}?`}
        rows={
          pending?.row
            ? [
                { label: 'Quantity', value: num(pending.row.held_quantity ?? 0) },
                { label: 'Avg price', value: inr(pending.row.avg_price ?? 0) },
                {
                  label: 'Unrealised P&L',
                  value: signedInr(pending.row.unrealized_pnl ?? 0),
                  tone: (pending.row.unrealized_pnl ?? 0) >= 0 ? 'up' : 'down',
                },
              ]
            : []
        }
        note={pending?.row ? `Why: ${pending.row.reasons.join(' · ')}. Resting exit orders are cancelled first.` : undefined}
        confirmLabel="Sell at market"
        busy={busy}
        onConfirm={confirmPending}
        onCancel={() => setPending(null)}
      />
    </div>
  )
}
