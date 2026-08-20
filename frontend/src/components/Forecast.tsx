import { useEffect, useRef, useState } from 'react'

import { api, type Forecast as ForecastData, type ForecastLean, type HorizonAccuracy, type NewsItem } from '../lib/api'
import { inr, pct, timeOf, toneClass } from '../lib/format'
import { SymbolSearch } from './SymbolSearch'

const DIRECTION_STYLE: Record<string, string> = {
  up: 'bg-emerald-500/15 text-emerald-400',
  down: 'bg-rose-500/15 text-rose-400',
  flat: 'bg-slate-600/30 text-slate-300',
}

const DIRECTION_ARROW: Record<string, string> = { up: '▲', down: '▼', flat: '◆' }

const CONFIDENCE_STYLE: Record<string, string> = {
  medium: 'bg-sky-500/15 text-sky-400',
  low: 'bg-amber-500/15 text-amber-400',
  none: 'bg-slate-700/40 text-slate-500',
}

/**
 * The Forecast tab: pick one share, get directional leans from seconds to the
 * next session — each with its drivers, news sentiment and the honest caveats.
 */
export function Forecast({ initialSymbol }: { initialSymbol?: string }) {
  const [symbol, setSymbol] = useState<string | null>(initialSymbol ?? null)
  const [data, setData] = useState<ForecastData | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [accuracy, setAccuracy] = useState<HorizonAccuracy[] | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    api
      .forecastAccuracy()
      .then(setAccuracy)
      .catch(() => setAccuracy(null)) // the strip simply hides when unavailable
  }, [])

  useEffect(() => {
    if (!symbol) return
    let alive = true
    setLoading(true)
    setError(null)
    api
      .forecast(symbol)
      .then((res) => {
        if (alive) setData(res)
      })
      .catch((err: Error) => {
        if (alive) setError(err.message)
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [symbol, attempt])

  return (
    <div className="space-y-4">
      <section className="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
        <div className="space-y-3">
          <SymbolSearch inputRef={searchRef} onPick={setSymbol} />
          <p className="px-1 text-[11px] text-slate-600">
            Search any NSE share to analyse its likely direction across horizons.
          </p>

          {data ? (
            <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
              <div className="flex items-baseline justify-between gap-2">
                <div>
                  <div className="text-lg font-semibold text-slate-100">{data.symbol}</div>
                  {data.name ? <div className="text-xs text-slate-400">{data.name}</div> : null}
                </div>
                <div className="text-right">
                  <div className="text-lg font-semibold tabular-nums text-slate-100">{inr(data.last_price)}</div>
                  <div className={`text-xs tabular-nums ${toneClass(data.change_pct)}`}>{pct(data.change_pct)}</div>
                </div>
              </div>
              <div className="mt-3 flex flex-wrap gap-2 text-[11px]">
                <span
                  className={`rounded-full px-2 py-0.5 font-medium ${
                    data.session_open ? 'bg-emerald-500/15 text-emerald-400' : 'bg-slate-700/40 text-slate-400'
                  }`}
                >
                  {data.session_open ? 'Market open' : 'Market closed'}
                </span>
                {data.price_source ? (
                  <span className="rounded-full bg-slate-700/40 px-2 py-0.5 font-medium text-slate-400">
                    prices: {data.price_source}
                  </span>
                ) : null}
                <span className="rounded-full bg-slate-700/40 px-2 py-0.5 font-medium text-slate-400">
                  as of {timeOf(data.as_of)}
                </span>
              </div>
            </div>
          ) : null}

          {data ? <NewsCard news={data.news} /> : null}

          <TrackRecord accuracy={accuracy} />
        </div>

        <div className="space-y-3">
          {error ? (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-rose-900/60 bg-rose-950/40 px-4 py-3 text-sm text-rose-300">
              <span>{error}</span>
              <button
                type="button"
                onClick={() => setAttempt((a) => a + 1)}
                disabled={loading}
                className="rounded-lg border border-rose-700/60 px-3 py-1.5 text-xs font-medium text-rose-200 transition-colors hover:bg-rose-900/40 disabled:opacity-50"
              >
                Retry
              </button>
            </div>
          ) : null}

          {!symbol ? (
            <div className="rounded-xl border border-dashed border-slate-800 px-6 py-16 text-center text-sm text-slate-500">
              Search for a share to see its forecast.
            </div>
          ) : loading && !data ? (
            <div className="rounded-xl border border-slate-800 px-6 py-16 text-center text-sm text-slate-500">
              Analysing {symbol}… fetching prices, history and news.
            </div>
          ) : null}

          {data ? data.leans.map((lean) => <LeanCard key={lean.horizon} lean={lean} />) : null}

          {data ? (
            <div className="rounded-xl border border-slate-800/70 bg-slate-900/40 px-4 py-3">
              <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-slate-500">
                Read this first
              </div>
              <ul className="list-disc space-y-1 pl-4 text-xs text-slate-400">
                {data.caveats.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      </section>
    </div>
  )
}

function LeanCard({ lean }: { lean: ForecastLean }) {
  const [open, setOpen] = useState(false)
  const hasDrivers = (lean.drivers?.length ?? 0) > 0

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <button
        type="button"
        onClick={() => hasDrivers && setOpen((v) => !v)}
        className={`flex w-full items-center gap-3 px-4 py-3 text-left ${hasDrivers ? 'cursor-pointer' : 'cursor-default'}`}
      >
        <span
          className={`inline-flex w-16 justify-center rounded-full px-2 py-1 text-xs font-semibold ${DIRECTION_STYLE[lean.direction]}`}
        >
          {DIRECTION_ARROW[lean.direction]} {lean.direction.toUpperCase()}
        </span>
        <span className="flex-1">
          <span className="block text-sm font-medium text-slate-100">{lean.label}</span>
          {lean.note ? <span className="mt-0.5 block text-[11px] leading-snug text-slate-500">{lean.note}</span> : null}
        </span>
        <span className="text-right">
          <span className="block text-sm font-semibold tabular-nums text-slate-100">
            {lean.probability_up.toFixed(0)}%
          </span>
          <span className="block text-[10px] text-slate-500">chance up</span>
        </span>
        <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${CONFIDENCE_STYLE[lean.confidence]}`}>
          {lean.confidence} conf.
        </span>
        {hasDrivers ? <span className="text-xs text-slate-500">{open ? '▾' : '▸'}</span> : null}
      </button>

      {open && hasDrivers ? (
        <div className="border-t border-slate-800/70 px-4 py-3">
          <div className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-slate-500">Why</div>
          <ul className="space-y-2">
            {lean.drivers!.map((d) => (
              <li key={d.name} className="flex items-start gap-3 text-xs">
                <span className={`mt-0.5 inline-block w-10 shrink-0 text-right font-semibold tabular-nums ${toneClass(d.score)}`}>
                  {d.score >= 0 ? '+' : ''}
                  {d.score.toFixed(2)}
                </span>
                <span>
                  <span className="font-medium text-slate-200">{d.name}</span>
                  <span className="text-slate-500"> · weight {(d.weight * 100).toFixed(0)}%</span>
                  <span className="block text-slate-400">{d.detail}</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}

function NewsCard({ news }: { news: ForecastData['news'] }) {
  const items = news.items ?? []
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[11px] font-semibold uppercase tracking-wide text-slate-500">News sentiment</span>
        {news.method !== 'none' ? (
          <span className="rounded-full bg-slate-700/40 px-2 py-0.5 text-[10px] font-medium text-slate-400">
            {news.method === 'claude' ? 'scored by Claude' : 'keyword lexicon'}
          </span>
        ) : null}
      </div>

      {items.length === 0 ? (
        <p className="text-xs text-slate-500">No recent headlines found.</p>
      ) : (
        <>
          <div className={`mb-2 text-sm font-semibold tabular-nums ${toneClass(news.overall)}`}>
            {news.overall >= 0 ? '+' : ''}
            {news.overall.toFixed(2)}{' '}
            <span className="text-[11px] font-normal text-slate-500">overall (-1 to +1, recency-weighted)</span>
          </div>
          <ul className="space-y-2">
            {items.slice(0, 6).map((it) => (
              <NewsRow key={it.title} item={it} />
            ))}
          </ul>
        </>
      )}

      {news.caveats?.length ? (
        <p className="mt-2 text-[10px] leading-snug text-slate-600">{news.caveats.join(' ')}</p>
      ) : null}
    </div>
  )
}

function NewsRow({ item }: { item: NewsItem }) {
  const title = item.url ? (
    <a href={item.url} target="_blank" rel="noreferrer" className="hover:underline">
      {item.title}
    </a>
  ) : (
    item.title
  )
  return (
    <li className="flex items-start gap-2 text-xs">
      <span className={`mt-0.5 w-10 shrink-0 text-right font-semibold tabular-nums ${toneClass(item.sentiment)}`}>
        {item.sentiment >= 0 ? '+' : ''}
        {item.sentiment.toFixed(1)}
      </span>
      <span className="text-slate-300">
        {title}
        {item.source ? <span className="block text-[10px] text-slate-600">{item.source}</span> : null}
      </span>
    </li>
  )
}

const HORIZON_LABEL: Record<string, string> = {
  intraday: 'Next ~15 min',
  close: 'By close',
  next_day: 'Next session',
}

/**
 * Measured accuracy of past forecasts — every directional lean is recorded
 * and scored once its horizon matures. If the numbers are unflattering, they
 * stay up anyway; that is the point.
 */
function TrackRecord({ accuracy }: { accuracy: HorizonAccuracy[] | null }) {
  if (!accuracy) return null
  const rows = accuracy.filter((a) => HORIZON_LABEL[a.horizon])
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <div className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-slate-500">
        Track record (measured)
      </div>
      {rows.length === 0 ? (
        <p className="text-xs text-slate-500">
          No scored forecasts yet. Every directional call is recorded and graded once its horizon passes —
          the real hit rate will appear here.
        </p>
      ) : (
        <ul className="space-y-1.5">
          {rows.map((a) => (
            <li key={a.horizon} className="flex items-baseline justify-between text-xs">
              <span className="text-slate-300">{HORIZON_LABEL[a.horizon]}</span>
              <span className="tabular-nums text-slate-400">
                <span className={a.hit_rate >= 0.5 ? 'font-semibold text-emerald-400' : 'font-semibold text-rose-400'}>
                  {(a.hit_rate * 100).toFixed(0)}%
                </span>{' '}
                right of {a.n}
                {a.n < 20 ? <span className="text-slate-600"> · small sample</span> : null}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-[10px] leading-snug text-slate-600">
        A coin flip scores 50%. Judge only samples of 20+ per horizon.
      </p>
    </div>
  )
}
