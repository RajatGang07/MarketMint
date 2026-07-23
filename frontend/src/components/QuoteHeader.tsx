import type { Quote } from '../lib/api'
import { compact, inr, num, pct, toneClass } from '../lib/format'

/** Selected instrument: name, live price, day change and the session's OHLC. */
export function QuoteHeader({ symbol, quote }: { symbol: string; quote?: Quote }) {
  const tone = toneClass(quote?.change ?? 0)

  const stats: Array<[string, string]> = quote
    ? [
        ['Open', num(quote.open)],
        ['High', num(quote.high)],
        ['Low', num(quote.low)],
        ['Prev close', num(quote.close)],
        ['Volume', quote.volume > 0 ? compact(quote.volume) : '—'],
      ]
    : []

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 px-4 py-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">{symbol}</h2>
            <span className="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-400">
              {quote?.exchange ?? 'NSE'}
            </span>
          </div>
          <p className="truncate text-xs text-slate-500">{quote?.name ?? '—'}</p>
        </div>

        <div className="text-right">
          <div className="text-2xl font-semibold tabular-nums">
            {quote ? inr(quote.last_price) : '—'}
          </div>
          {quote ? (
            <div className={`text-sm font-medium tabular-nums ${tone}`}>
              {quote.change >= 0 ? '+' : ''}
              {num(quote.change)} ({pct(quote.change_pct)})
            </div>
          ) : null}
        </div>
      </div>

      {stats.length > 0 ? (
        <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-slate-800 pt-3 text-xs sm:grid-cols-5">
          {stats.map(([label, value]) => (
            <div key={label} className="flex justify-between sm:block">
              <dt className="text-slate-500">{label}</dt>
              <dd className="tabular-nums text-slate-200 sm:mt-0.5">{value}</dd>
            </div>
          ))}
        </dl>
      ) : null}
    </div>
  )
}
