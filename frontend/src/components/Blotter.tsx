import { useState } from 'react'

import type { Order, Position, Trade } from '../lib/api'
import { OrdersTable } from './OrdersTable'
import { Positions } from './Positions'
import { TradesTable } from './TradesTable'

type Tab = 'positions' | 'open' | 'orders' | 'trades'

/**
 * The account blotter: holdings, working orders, the full order log and the
 * executed trades, in one panel so they never compete for vertical space.
 */
export function Blotter({
  positions,
  orders,
  trades,
  onCancel,
  onSelectSymbol,
}: {
  positions: Position[]
  orders: Order[]
  trades: Trade[]
  onCancel: (orderRef: string) => void
  onSelectSymbol: (symbol: string) => void
}) {
  const [tab, setTab] = useState<Tab>('positions')
  const openOrders = orders.filter((o) => o.status === 'OPEN')

  const tabs: Array<[Tab, string, number]> = [
    ['positions', 'Positions', positions.length],
    ['open', 'Open orders', openOrders.length],
    ['orders', 'Order history', orders.length],
    ['trades', 'Trades', trades.length],
  ]

  return (
    <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap gap-1 border-b border-slate-800 px-2 py-2" role="tablist">
        {tabs.map(([id, label, count]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
            className={`rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
              tab === id ? 'bg-slate-800 text-slate-100' : 'text-slate-400 hover:bg-slate-800/60'
            }`}
          >
            {label}
            {count > 0 ? <span className="ml-1.5 text-xs text-slate-500">{count}</span> : null}
          </button>
        ))}
      </div>

      {tab === 'positions' ? <Positions positions={positions} onSelect={onSelectSymbol} /> : null}
      {tab === 'open' ? (
        <OrdersTable
          orders={openOrders}
          onCancel={onCancel}
          emptyMessage="No working orders. Limit orders rest here until they fill."
        />
      ) : null}
      {tab === 'orders' ? <OrdersTable orders={orders} onCancel={onCancel} /> : null}
      {tab === 'trades' ? <TradesTable trades={trades} /> : null}
    </div>
  )
}
