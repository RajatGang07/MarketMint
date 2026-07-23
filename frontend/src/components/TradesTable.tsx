import type { Trade } from '../lib/api'
import { dateTimeOf, inr, num, signedInr, toneClass } from '../lib/format'

/** Executed fills, newest first, with the realised P&L each sell booked. */
export function TradesTable({ trades }: { trades: Trade[] }) {
  if (trades.length === 0) {
    return <p className="px-4 py-8 text-center text-sm text-slate-500">No trades yet.</p>
  }

  return (
    <div className="max-h-80 overflow-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-slate-900">
          <tr className="text-left text-xs uppercase tracking-wide text-slate-500">
            <th className="px-4 py-2 font-medium">Time</th>
            <th className="px-4 py-2 font-medium">Side</th>
            <th className="px-4 py-2 font-medium">Symbol</th>
            <th className="px-4 py-2 text-right font-medium">Qty</th>
            <th className="px-4 py-2 text-right font-medium">Price</th>
            <th className="px-4 py-2 text-right font-medium">Realised</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-800/70">
          {trades.map((t) => (
            <tr key={t.id} className="tabular-nums">
              <td className="px-4 py-2.5 text-slate-400">{dateTimeOf(t.created_at)}</td>
              <td
                className={`px-4 py-2.5 font-medium ${
                  t.transaction_type === 'BUY' ? 'text-emerald-400' : 'text-rose-400'
                }`}
              >
                {t.transaction_type}
              </td>
              <td className="px-4 py-2.5 font-medium">{t.trading_symbol}</td>
              <td className="px-4 py-2.5 text-right text-slate-300">{num(t.quantity)}</td>
              <td className="px-4 py-2.5 text-right text-slate-300">{inr(t.price)}</td>
              <td className={`px-4 py-2.5 text-right font-medium ${toneClass(t.realized_pnl)}`}>
                {t.transaction_type === 'SELL' ? signedInr(t.realized_pnl) : '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
