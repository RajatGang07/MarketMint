import type { Position } from '../lib/api'
import { inr, num, pct, signedInr, toneClass } from '../lib/format'

/** Open holdings marked to market. Clicking a row selects that symbol. */
export function Positions({
  positions,
  onSelect,
}: {
  positions: Position[]
  onSelect: (symbol: string) => void
}) {
  if (positions.length === 0) {
    return (
      <p className="px-4 py-8 text-center text-sm text-slate-500">
        No open positions. Place a buy order to get started.
      </p>
    )
  }

  return (
    <div className="max-h-80 overflow-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-slate-900">
          <tr className="text-left text-xs uppercase tracking-wide text-slate-500">
            <th className="px-4 py-2 font-medium">Symbol</th>
            <th className="px-4 py-2 text-right font-medium">Qty</th>
            <th className="px-4 py-2 text-right font-medium">Avg</th>
            <th className="px-4 py-2 text-right font-medium">LTP</th>
            <th className="px-4 py-2 text-right font-medium">Value</th>
            <th className="px-4 py-2 text-right font-medium">Unrealised</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-800/70">
          {positions.map((p) => {
            // Percent return on this holding's own cost, not on the portfolio.
            const cost = p.avg_price * p.quantity
            const pnlPct = cost > 0 ? (p.unrealized_pnl / cost) * 100 : 0
            return (
              <tr
                key={`${p.trading_symbol}-${p.segment}`}
                onClick={() => onSelect(p.trading_symbol)}
                className="cursor-pointer tabular-nums transition-colors hover:bg-slate-800/50"
              >
                <td className="px-4 py-2.5 font-medium">{p.trading_symbol}</td>
                <td className="px-4 py-2.5 text-right text-slate-300">{num(p.quantity)}</td>
                <td className="px-4 py-2.5 text-right text-slate-300">{inr(p.avg_price)}</td>
                <td className="px-4 py-2.5 text-right text-slate-300">{inr(p.ltp)}</td>
                <td className="px-4 py-2.5 text-right text-slate-300">{inr(p.market_value)}</td>
                <td className={`px-4 py-2.5 text-right font-medium ${toneClass(p.unrealized_pnl)}`}>
                  {signedInr(p.unrealized_pnl)}
                  <span className="ml-1 text-xs opacity-80">{pct(pnlPct)}</span>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
