import { useEffect, useState } from 'react'

import { api, type OrderRequest } from '../lib/api'
import { inr, num } from '../lib/format'

type Side = 'BUY' | 'SELL'
type OrderType = 'MARKET' | 'LIMIT'

export function OrderTicket({
  symbol,
  ltp,
  cash,
  heldQuantity,
  onPlaced,
}: {
  symbol: string
  ltp?: number
  /** Available paper cash, for the buying-power check. */
  cash?: number
  /** Quantity currently held, since v1 is long-only and can't short. */
  heldQuantity: number
  onPlaced: () => void
}) {
  const [side, setSide] = useState<Side>('BUY')
  const [orderType, setOrderType] = useState<OrderType>('MARKET')
  const [qty, setQty] = useState('1')
  const [limit, setLimit] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  // Switching instrument invalidates the limit price and any stale receipt.
  useEffect(() => {
    setLimit('')
    setMsg(null)
  }, [symbol])

  const quantity = Math.max(0, Math.floor(Number(qty) || 0))
  const limitPrice = Number(limit) || 0
  const priceForEstimate = orderType === 'LIMIT' ? limitPrice : (ltp ?? 0)
  const estimate = quantity * priceForEstimate

  // Client-side pre-checks. The server re-validates and is the authority; these
  // exist so the user isn't told "rejected" for something obvious.
  const notEnoughCash = side === 'BUY' && cash != null && estimate > cash
  const notEnoughShares = side === 'SELL' && quantity > heldQuantity
  const needsLimit = orderType === 'LIMIT' && limitPrice <= 0

  const blocker = notEnoughCash
    ? `Needs ${inr(estimate)}, you have ${inr(cash ?? 0)}`
    : notEnoughShares
      ? `You hold ${num(heldQuantity)} ${symbol} — shorting isn't supported`
      : needsLimit
        ? 'Enter a limit price'
        : null

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setMsg(null)
    try {
      const body: OrderRequest = {
        trading_symbol: symbol,
        transaction_type: side,
        order_type: orderType,
        quantity,
        limit_price: orderType === 'LIMIT' ? limitPrice : null,
      }
      const order = await api.placeOrder(body)
      setMsg({
        ok: order.status !== 'REJECTED',
        text: `${order.status}${order.message ? ` — ${order.message}` : ''}`,
      })
      onPlaced()
    } catch (err) {
      setMsg({ ok: false, text: (err as Error).message })
    } finally {
      setBusy(false)
    }
  }

  const sideBtn = (value: Side, label: string) => {
    const active = side === value
    const activeClass = value === 'BUY' ? 'bg-emerald-600 text-white' : 'bg-rose-600 text-white'
    return (
      <button
        type="button"
        onClick={() => setSide(value)}
        aria-pressed={active}
        className={`flex-1 rounded-lg py-2 text-sm font-semibold transition-colors ${
          active ? activeClass : 'bg-slate-800 text-slate-300 hover:bg-slate-700'
        }`}
      >
        {label}
      </button>
    )
  }

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex items-center justify-between border-b border-slate-800 px-4 py-3">
        <span className="text-sm font-semibold">Order ticket</span>
        <span className="rounded bg-slate-800 px-2 py-0.5 text-xs font-medium text-slate-300 tabular-nums">
          {symbol} · {ltp != null ? inr(ltp) : '—'}
        </span>
      </div>

      <form onSubmit={submit} className="space-y-3 p-4">
        <div className="flex gap-2">
          {sideBtn('BUY', 'Buy')}
          {sideBtn('SELL', 'Sell')}
        </div>

        <div className="grid grid-cols-2 gap-2">
          <label className="text-xs text-slate-400">
            Order type
            <select
              value={orderType}
              onChange={(e) => setOrderType(e.target.value as OrderType)}
              className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 focus:border-slate-500 focus:outline-none"
            >
              <option value="MARKET">Market</option>
              <option value="LIMIT">Limit</option>
            </select>
          </label>

          <label className="text-xs text-slate-400">
            Quantity
            <input
              type="number"
              min={1}
              step={1}
              value={qty}
              onChange={(e) => setQty(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 focus:border-slate-500 focus:outline-none"
            />
          </label>
        </div>

        {orderType === 'LIMIT' ? (
          <label className="block text-xs text-slate-400">
            Limit price
            <div className="mt-1 flex gap-2">
              <input
                type="number"
                min={0}
                step="0.05"
                value={limit}
                onChange={(e) => setLimit(e.target.value)}
                placeholder={ltp != null ? String(ltp) : '0.00'}
                className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 focus:border-slate-500 focus:outline-none"
              />
              {ltp != null ? (
                <button
                  type="button"
                  onClick={() => setLimit(String(ltp))}
                  className="shrink-0 rounded-lg border border-slate-700 px-2 text-xs text-slate-400 hover:bg-slate-800"
                >
                  LTP
                </button>
              ) : null}
            </div>
          </label>
        ) : null}

        <dl className="space-y-1 text-xs">
          <div className="flex items-center justify-between">
            <dt className="text-slate-400">Estimated value</dt>
            <dd className="tabular-nums text-slate-200">{inr(estimate)}</dd>
          </div>
          <div className="flex items-center justify-between">
            <dt className="text-slate-400">{side === 'BUY' ? 'Available cash' : 'Holding'}</dt>
            <dd className="tabular-nums text-slate-200">
              {side === 'BUY' ? inr(cash ?? 0) : `${num(heldQuantity)} qty`}
            </dd>
          </div>
        </dl>

        <button
          type="submit"
          disabled={busy || quantity <= 0 || blocker !== null}
          className={`w-full rounded-lg py-2.5 text-sm font-semibold text-white transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
            side === 'BUY' ? 'bg-emerald-600 hover:bg-emerald-500' : 'bg-rose-600 hover:bg-rose-500'
          }`}
        >
          {busy ? 'Placing…' : `${side === 'BUY' ? 'Buy' : 'Sell'} ${quantity || ''} ${symbol}`}
        </button>

        {blocker ? <p className="text-xs text-amber-400">{blocker}</p> : null}

        {msg ? (
          <div
            className={`rounded-lg px-3 py-2 text-xs ${
              msg.ok ? 'bg-emerald-500/15 text-emerald-300' : 'bg-rose-500/15 text-rose-300'
            }`}
          >
            {msg.text}
          </div>
        ) : null}
      </form>
    </div>
  )
}
