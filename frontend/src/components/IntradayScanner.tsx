import { useEffect, useState } from 'react'

import { api, type IntradayPick, type IntradayResult } from '../lib/api'
import { inr, num, timeOf } from '../lib/format'
import { useToast } from '../lib/toast'
import { ConfirmDialog } from './ConfirmDialog'

const STATUS_STYLE: Record<string, string> = {
  ACTIVE: 'bg-emerald-500/15 text-emerald-400',
  TARGET: 'bg-sky-500/15 text-sky-400',
  TRAIL: 'bg-teal-500/15 text-teal-300',
  STOP: 'bg-rose-500/15 text-rose-400',
  SQUARE_OFF: 'bg-slate-600/30 text-slate-400',
}

const STATUS_LABEL: Record<string, string> = {
  ACTIVE: 'Active',
  TARGET: 'Hit target',
  TRAIL: 'Trailed out',
  STOP: 'Stopped',
  SQUARE_OFF: 'Squared off',
}

/**
 * Intraday ORB scanner. Entries come from the backend rule; exits are fully
 * mechanical — initial stop, 2R target, 1R trailing stop, and the 15:15
 * square-off — and are enforced server-side once a trade is placed.
 */
export function IntradayScanner({
  active,
  onTraded,
  onSelectSymbol,
}: {
  active: boolean
  onTraded: () => void
  onSelectSymbol: (symbol: string) => void
}) {
  const toast = useToast()
  const [data, setData] = useState<IntradayResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<IntradayPick | null>(null)
  const [busy, setBusy] = useState(false)

  async function runScan() {
    setLoading(true)
    setError(null)
    try {
      setData(await api.intraday())
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  // Fetch on first visit instead of hiding behind a button.
  useEffect(() => {
    if (active && !data && !loading && !error) void runScan()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active])

  async function confirmTrade() {
    const pick = pending
    if (!pick?.quantity) return
    setBusy(true)
    try {
      const order = await api.placeOrder({
        trading_symbol: pick.symbol,
        transaction_type: 'BUY',
        order_type: 'MARKET',
        product: 'MIS',
        quantity: pick.quantity,
        stop_loss: pick.stop,
        target: pick.target,
        trail_by: pick.trail_by,
      })
      toast.push(
        order.status === 'FILLED' ? 'success' : 'error',
        `${pick.symbol}: ${order.status}${order.status === 'FILLED' ? ' — trail + target armed, square-off 15:15' : ''}`,
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
          <span className="text-sm font-semibold">Intraday scanner</span>
          <span className="ml-2 text-xs text-slate-500">
            opening-range breakout · 5-min bars · exits: stop / 2R target / 1R trail / 15:15 square-off
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

      {error ? <div className="px-4 py-3 text-sm text-rose-300">{error}</div> : null}

      {!data && loading ? (
        <div className="px-4 py-10 text-center text-sm text-slate-500">
          <div className="mx-auto mb-3 h-5 w-5 animate-spin rounded-full border-2 border-slate-600 border-t-slate-200" />
          Replaying the breakout rule over a month of 5-minute bars for ~200 stocks — first run takes a minute…
        </div>
      ) : null}

      {data ? (
        <>
          <div
            className={`border-b border-slate-800 px-4 py-2.5 text-xs ${
              data.session_open ? 'bg-emerald-950/30 text-emerald-300' : 'bg-amber-950/30 text-amber-300'
            }`}
          >
            {data.session_note} Session: {data.session_date} · {data.triggered_today} of {data.with_data} stocks
            triggered.
          </div>

          {bt && bt.trades > 0 ? (
            <div className="border-b border-slate-800 bg-slate-950/40 px-4 py-2.5 text-xs text-slate-400">
              Backtest, same rule, {data.backtest_sessions} sessions × {data.with_data} stocks:{' '}
              <span className="tabular-nums text-slate-200">{bt.trades}</span> trades · win rate{' '}
              <span className="tabular-nums text-slate-200">{(bt.win_rate * 100).toFixed(0)}%</span> · avg{' '}
              <span className={`tabular-nums ${bt.avg_r >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                {bt.avg_r >= 0 ? '+' : ''}
                {bt.avg_r.toFixed(2)}R
              </span>{' '}
              · avg win <span className="tabular-nums text-slate-200">+{bt.avg_win_r.toFixed(2)}R</span> · avg loss{' '}
              <span className="tabular-nums text-slate-200">{bt.avg_loss_r.toFixed(2)}R</span> · profit factor{' '}
              <span className="tabular-nums text-slate-200">{bt.profit_factor.toFixed(2)}</span>
              <span className="ml-1 text-slate-600">(1R = your per-trade risk, default ₹5,000)</span>
            </div>
          ) : null}

          {data.picks.length === 0 ? (
            <p className="px-4 py-8 text-center text-sm text-slate-500">
              No breakout signals this session — most days only a handful of stocks set up, and forcing a trade is
              how the loss column grows.
            </p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-slate-500">
                    <th className="px-4 py-2 font-medium">Signal</th>
                    <th className="px-4 py-2 text-right font-medium">Entry</th>
                    <th className="px-4 py-2 text-right font-medium">Stop now</th>
                    <th className="px-4 py-2 text-right font-medium">Target</th>
                    <th className="px-4 py-2 text-right font-medium">RVOL</th>
                    <th className="px-4 py-2 text-right font-medium">History (20d)</th>
                    <th className="px-4 py-2 text-right font-medium">Outcome</th>
                    <th className="px-4 py-2 text-right font-medium"></th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/70">
                  {data.picks.map((pick) => (
                    <tr key={pick.symbol} className="tabular-nums">
                      <td className="px-4 py-2.5">
                        <button
                          type="button"
                          onClick={() => onSelectSymbol(pick.symbol)}
                          className="text-left"
                          title="Show on chart"
                        >
                          <span className="font-medium text-slate-100">{pick.symbol}</span>
                          <span
                            className={`ml-2 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                              STATUS_STYLE[pick.status] ?? ''
                            }`}
                          >
                            {STATUS_LABEL[pick.status] ?? pick.status}
                          </span>
                          <span className="block text-xs text-slate-500">
                            {timeOf(pick.entry_time)} · OR {num(pick.or_high)} · VWAP {num(pick.vwap)}
                          </span>
                        </button>
                      </td>
                      <td className="px-4 py-2.5 text-right text-slate-300">{num(pick.entry)}</td>
                      <td className="px-4 py-2.5 text-right text-slate-300">{num(pick.stop)}</td>
                      <td className="px-4 py-2.5 text-right text-slate-300">{num(pick.target)}</td>
                      <td className="px-4 py-2.5 text-right text-slate-300">{num(pick.rvol)}×</td>
                      <td className="px-4 py-2.5 text-right text-xs text-slate-400">
                        {pick.history.trades > 0 ? (
                          <>
                            {pick.history.trades} tr ·{' '}
                            <span className="text-slate-200">{(pick.history.win_rate * 100).toFixed(0)}%</span> ·{' '}
                            <span className={pick.history.avg_r >= 0 ? 'text-emerald-400' : 'text-rose-400'}>
                              {pick.history.avg_r >= 0 ? '+' : ''}
                              {pick.history.avg_r.toFixed(2)}R
                            </span>
                          </>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        {pick.status === 'ACTIVE' ? (
                          <span className="text-xs text-slate-400">running · LTP {num(pick.last_price)}</span>
                        ) : (
                          <span
                            className={`text-xs ${
                              (pick.result_r ?? 0) >= 0 ? 'text-emerald-400' : 'text-rose-400'
                            }`}
                          >
                            {pick.exit_time ? timeOf(pick.exit_time) : ''} @ {num(pick.exit ?? 0)} (
                            {(pick.result_r ?? 0) >= 0 ? '+' : ''}
                            {(pick.result_r ?? 0).toFixed(2)}R)
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        {pick.status === 'ACTIVE' && data.session_open && pick.quantity ? (
                          <button
                            type="button"
                            onClick={() => setPending(pick)}
                            className="rounded-lg bg-emerald-600 px-3 py-1 text-xs font-semibold text-white hover:bg-emerald-500"
                            title={`Buy ${pick.quantity} · risk ${inr(pick.max_loss ?? 0)}`}
                          >
                            Buy {pick.quantity}
                          </button>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <p className="border-t border-slate-800 px-4 py-2 text-[11px] text-slate-600">
            {data.rule} {data.caveats.join(' ')} Prices via {data.price_source}.
          </p>
        </>
      ) : null}

      <ConfirmDialog
        open={pending !== null}
        title={`Intraday buy ${pending?.symbol ?? ''}`}
        rows={
          pending
            ? [
                { label: 'Quantity', value: num(pending.quantity ?? 0) },
                { label: 'Entry (market)', value: `~${inr(pending.entry)}` },
                { label: 'Capital', value: inr(pending.capital_required ?? 0) },
                { label: 'Initial stop', value: inr(pending.stop), tone: 'down' },
                { label: 'Max loss', value: inr(pending.max_loss ?? 0), tone: 'down' },
                { label: 'Target (2R)', value: inr(pending.target), tone: 'up' },
                { label: 'Trails high by', value: inr(pending.trail_by) },
              ]
            : []
        }
        note="The stop ratchets up as the price makes new highs, and anything still open squares off at 15:15 IST. All exits run server-side."
        confirmLabel="Place intraday order"
        busy={busy}
        onConfirm={confirmTrade}
        onCancel={() => setPending(null)}
      />
    </div>
  )
}
