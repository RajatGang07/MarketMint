import { useMemo, useRef, useState } from 'react'

import { api } from '../lib/api'
import { computeCharges, type Product } from '../lib/charges'
import { inr, signedInr } from '../lib/format'
import { SymbolSearch } from './SymbolSearch'

const PRODUCTS: Array<[Product, string, string]> = [
  ['delivery', 'Delivery (CNC)', 'buy today, hold in demat'],
  ['intraday', 'Intraday (MIS)', 'square off the same day'],
  ['mtf', 'MTF / Pay Later', 'broker funds part of the buy'],
]

/**
 * The Charges tab: the full cost bifurcation of a real trade — brokerage,
 * STT, exchange charges, SEBI fee, stamp duty, GST, DP charge and MTF
 * interest — with break-even and net P&L. The paper engine fills at clean
 * prices; this tab shows what the same trade costs with a real broker.
 */
export function Charges() {
  const [product, setProduct] = useState<Product>('delivery')
  const [symbol, setSymbol] = useState<string | null>(null)
  const [buyPrice, setBuyPrice] = useState('1000')
  const [quantity, setQuantity] = useState('100')
  const [sellPrice, setSellPrice] = useState('')
  const [mtfDays, setMtfDays] = useState('7')
  const searchRef = useRef<HTMLInputElement>(null)

  async function pickSymbol(sym: string) {
    setSymbol(sym)
    try {
      const q = await api.quote(sym)
      if (q.ok && q.last_price > 0) setBuyPrice(String(q.last_price))
    } catch {
      // Price prefill is a convenience; manual entry still works.
    }
  }

  const result = useMemo(() => {
    const buy = Number(buyPrice)
    const qty = Number(quantity)
    if (!Number.isFinite(buy) || buy <= 0 || !Number.isFinite(qty) || qty < 1) return null
    return computeCharges({
      product,
      buyPrice: buy,
      quantity: qty,
      sellPrice: sellPrice ? Number(sellPrice) : null,
      mtfDays: Number(mtfDays) || 1,
    })
  }, [product, buyPrice, quantity, sellPrice, mtfDays])

  const inputCls =
    'mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-slate-500'

  return (
    <div className="mx-auto max-w-5xl space-y-4">
      <section className="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
        {/* Inputs */}
        <div className="space-y-3">
          <SymbolSearch inputRef={searchRef} onPick={pickSymbol} />
          <p className="px-1 text-[11px] text-slate-600">
            Optional: pick a share to prefill its live price{symbol ? ` (using ${symbol})` : ''}.
          </p>

          <div className="space-y-3 rounded-xl border border-slate-800 bg-slate-900/60 p-4">
            <div className="flex flex-wrap gap-1 rounded-lg border border-slate-800 bg-slate-950/60 p-1">
              {PRODUCTS.map(([id, label, hint]) => (
                <button
                  key={id}
                  type="button"
                  onClick={() => setProduct(id)}
                  className={`flex-1 rounded-md px-2 py-1.5 text-left transition-colors ${
                    product === id ? 'bg-slate-800 text-slate-100' : 'text-slate-400 hover:bg-slate-800/50'
                  }`}
                >
                  <span className="block text-xs font-medium">{label}</span>
                  <span className="block text-[9px] text-slate-500">{hint}</span>
                </button>
              ))}
            </div>

            <label className="block text-xs text-slate-400">
              Buy price (₹)
              <input type="number" min={0} step="0.05" value={buyPrice} onChange={(e) => setBuyPrice(e.target.value)} className={inputCls} />
            </label>
            <label className="block text-xs text-slate-400">
              Quantity
              <input type="number" min={1} value={quantity} onChange={(e) => setQuantity(e.target.value)} className={inputCls} />
            </label>
            <label className="block text-xs text-slate-400">
              Sell price (₹) <span className="text-slate-600">— optional, completes the round trip</span>
              <input
                type="number"
                min={0}
                step="0.05"
                value={sellPrice}
                onChange={(e) => setSellPrice(e.target.value)}
                placeholder="leave empty for buy-side estimate"
                className={inputCls}
              />
            </label>
            {product === 'mtf' ? (
              <label className="block text-xs text-slate-400">
                Days held (MTF interest accrues daily)
                <input type="number" min={1} value={mtfDays} onChange={(e) => setMtfDays(e.target.value)} className={inputCls} />
              </label>
            ) : null}
          </div>

          <div className="rounded-xl border border-slate-800/70 bg-slate-900/40 p-4 text-[11px] leading-relaxed text-slate-500">
            Rates follow Groww&apos;s published schedule and NSE/statutory rates (mid-2025): brokerage 0.1% capped at
            ₹20 (min ₹5) per order, delivery STT 0.1% both sides, intraday STT 0.025% on sell, NSE 0.00297%, SEBI ₹10/crore,
            stamp 0.015%/0.003% on buy, 18% GST, DP ₹18.50 on delivery sells, MTF ~14.99% p.a. on the funded ~75%.
            Brokers revise these — verify against Groww&apos;s current schedule before relying on the totals.
          </div>
        </div>

        {/* Breakdown */}
        <div className="space-y-3">
          {result ? (
            <>
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
                  <div className="text-[10px] uppercase tracking-wide text-slate-500">Total charges</div>
                  <div className="mt-1 text-lg font-semibold tabular-nums text-amber-400">{inr(result.totalCharges)}</div>
                  <div className="text-[10px] text-slate-500">{result.chargesPctOfTurnover}% of turnover</div>
                </div>
                <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
                  <div className="text-[10px] uppercase tracking-wide text-slate-500">Break-even sell price</div>
                  <div className="mt-1 text-lg font-semibold tabular-nums text-slate-100">{inr(result.breakEvenPrice)}</div>
                  <div className="text-[10px] text-slate-500">covers every charge below</div>
                </div>
                <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
                  <div className="text-[10px] uppercase tracking-wide text-slate-500">Net P&amp;L</div>
                  {result.netPnL !== null ? (
                    <>
                      <div className={`mt-1 text-lg font-semibold tabular-nums ${result.netPnL >= 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
                        {signedInr(result.netPnL)}
                      </div>
                      <div className="text-[10px] text-slate-500">
                        gross {signedInr(result.grossPnL ?? 0)} − charges {inr(result.totalCharges)}
                      </div>
                    </>
                  ) : (
                    <div className="mt-1 text-sm text-slate-500">enter a sell price</div>
                  )}
                </div>
              </div>

              <div className="rounded-xl border border-slate-800 bg-slate-900/60">
                <div className="flex items-baseline justify-between border-b border-slate-800/70 px-4 py-3">
                  <span className="text-sm font-semibold text-slate-100">Charge bifurcation</span>
                  <span className="text-xs tabular-nums text-slate-500">
                    buy {inr(result.buyValue)}
                    {result.sellValue !== null ? ` · sell ${inr(result.sellValue)}` : ''}
                  </span>
                </div>
                <ul className="divide-y divide-slate-800/60">
                  {result.lines.map((l) => (
                    <li key={l.name} className="flex items-start justify-between gap-4 px-4 py-2.5">
                      <div className="min-w-0">
                        <div className="text-sm text-slate-200">{l.name}</div>
                        <div className="text-[11px] text-slate-500">{l.basis}</div>
                      </div>
                      <div className="shrink-0 text-sm font-medium tabular-nums text-slate-100">{inr(l.amount)}</div>
                    </li>
                  ))}
                  <li className="flex items-center justify-between px-4 py-3">
                    <span className="text-sm font-semibold text-slate-100">Total</span>
                    <span className="text-sm font-semibold tabular-nums text-amber-400">{inr(result.totalCharges)}</span>
                  </li>
                </ul>
              </div>

              {result.sellValue === null ? (
                <p className="px-1 text-[11px] text-slate-600">
                  Sell-side charges are estimated at the buy price until you enter a sell price.
                </p>
              ) : null}
            </>
          ) : (
            <div className="rounded-xl border border-dashed border-slate-800 px-6 py-16 text-center text-sm text-slate-500">
              Enter a valid buy price and quantity to see the charge breakdown.
            </div>
          )}
        </div>
      </section>
    </div>
  )
}
