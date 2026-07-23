import type { Order } from '../lib/api'
import { dateTimeOf, inr, num } from '../lib/format'

const STATUS_STYLE: Record<string, string> = {
  FILLED: 'bg-emerald-500/15 text-emerald-400',
  OPEN: 'bg-amber-500/15 text-amber-400',
  REJECTED: 'bg-rose-500/15 text-rose-400',
  CANCELLED: 'bg-slate-600/30 text-slate-400',
}

export function OrdersTable({
  orders,
  onCancel,
  emptyMessage = 'No orders yet.',
}: {
  orders: Order[]
  onCancel: (orderRef: string) => void
  emptyMessage?: string
}) {
  if (orders.length === 0) {
    return <p className="px-4 py-8 text-center text-sm text-slate-500">{emptyMessage}</p>
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
            <th className="px-4 py-2 text-right font-medium">Status</th>
            <th className="px-4 py-2 text-right font-medium"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-800/70">
          {orders.map((o) => (
            <tr key={o.id} className="tabular-nums" title={o.message ?? undefined}>
              <td className="px-4 py-2.5 text-slate-400">{dateTimeOf(o.created_at)}</td>
              <td className="px-4 py-2.5">
                <span className={o.transaction_type === 'BUY' ? 'text-emerald-400' : 'text-rose-400'}>
                  {o.transaction_type}
                </span>
                <span className="ml-1 text-xs text-slate-500">{o.order_type}</span>
              </td>
              <td className="px-4 py-2.5 font-medium">{o.trading_symbol}</td>
              <td className="px-4 py-2.5 text-right text-slate-300">{num(o.quantity)}</td>
              <td className="px-4 py-2.5 text-right text-slate-300">
                {o.fill_price != null
                  ? inr(o.fill_price)
                  : o.order_type === 'SL' && o.trigger_price != null
                    ? `${inr(o.trigger_price)} (SL)`
                    : o.limit_price != null
                      ? `${inr(o.limit_price)} (L)`
                      : '—'}
              </td>
              <td className="px-4 py-2.5 text-right">
                <span
                  className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${
                    STATUS_STYLE[o.status] ?? 'bg-slate-600/30 text-slate-400'
                  }`}
                >
                  {o.status}
                </span>
              </td>
              <td className="px-4 py-2.5 text-right">
                {o.status === 'OPEN' ? (
                  <button
                    type="button"
                    onClick={() => onCancel(o.order_ref)}
                    className="rounded border border-slate-700 px-2 py-0.5 text-xs text-slate-400 transition-colors hover:bg-slate-800 hover:text-slate-200"
                  >
                    Cancel
                  </button>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
