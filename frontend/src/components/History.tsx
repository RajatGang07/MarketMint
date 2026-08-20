import { useEffect, useMemo, useRef, useState } from 'react'

import { api, type Candle } from '../lib/api'
import { compact, inr, pct, signedInr, toneClass } from '../lib/format'
import { SymbolSearch } from './SymbolSearch'

// All dates render on the IST calendar: daily bars are stamped at the NSE
// session open, and a browser west of India would otherwise show them a day
// early.
const istDayFmt = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Kolkata',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})
const displayDateFmt = new Intl.DateTimeFormat('en-IN', {
  timeZone: 'Asia/Kolkata',
  day: '2-digit',
  month: 'short',
  year: 'numeric',
})
const weekdayFmt = new Intl.DateTimeFormat('en-IN', { timeZone: 'Asia/Kolkata', weekday: 'short' })

/** yyyy-mm-dd key for a bar's session, on the IST calendar. */
const dayKey = (iso: string): string => istDayFmt.format(new Date(iso))

/** yyyy-mm-dd for <input type="date"> values. */
function isoDay(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function daysAgo(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return isoDay(d)
}

/**
 * Presets fetch by calendar lookback; `sessions` trims to the most recent N
 * trading days so "10 days" means ten sessions, not ten calendar days minus
 * weekends.
 */
const PRESETS = [
  { id: '10d', label: '10 days', days: 18, sessions: 10 },
  { id: '1mo', label: '1 month', days: 31 },
  { id: '3mo', label: '3 months', days: 92 },
  { id: '6mo', label: '6 months', days: 183 },
  { id: '1y', label: '1 year', days: 366 },
] as const

type PresetId = (typeof PRESETS)[number]['id'] | 'custom'

interface Row extends Candle {
  change: number | null
  changePct: number | null
}

/**
 * The History tab: pick a share, get its day-by-day OHLC, change and volume —
 * last 10 sessions by default, any date window on request.
 */
export function History({ initialSymbol }: { initialSymbol?: string }) {
  const [symbol, setSymbol] = useState<string | null>(initialSymbol ?? null)
  const [preset, setPreset] = useState<PresetId>('10d')
  const [customFrom, setCustomFrom] = useState<string>(() => daysAgo(30))
  const [customTo, setCustomTo] = useState<string>(() => isoDay(new Date()))
  const [applied, setApplied] = useState<{ from: string; to: string } | null>(null)
  const [rows, setRows] = useState<Row[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  const today = isoDay(new Date())

  const span = useMemo(() => {
    if (preset === 'custom') {
      return applied ? { ...applied, sessions: undefined } : null
    }
    const p = PRESETS.find((x) => x.id === preset)!
    return { from: daysAgo(p.days), to: today, sessions: 'sessions' in p ? p.sessions : undefined }
    // `today` only changes at midnight; recomputing per render is harmless.
  }, [preset, applied, today])

  useEffect(() => {
    if (!symbol || !span) return
    let alive = true
    setLoading(true)
    setError(null)

    // Over-fetch a week so the first visible row still has a previous close
    // to measure its day change against.
    const pad = new Date(`${span.from}T00:00:00`)
    pad.setDate(pad.getDate() - 7)

    api
      .dailyHistory(symbol, isoDay(pad), span.to)
      .then((res) => {
        if (!alive) return
        const sorted = [...res.candles].sort((a, b) => +new Date(a.time) - +new Date(b.time))
        const all: Row[] = sorted.map((c, i) => {
          const prev = i > 0 ? sorted[i - 1].close : null
          return {
            ...c,
            change: prev != null ? c.close - prev : null,
            changePct: prev != null && prev !== 0 ? ((c.close - prev) / prev) * 100 : null,
          }
        })
        let visible = all.filter((r) => dayKey(r.time) >= span.from)
        if (span.sessions) visible = visible.slice(-span.sessions)
        setRows(visible.reverse()) // newest first
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
  }, [symbol, span])

  const summary = useMemo(() => {
    if (!rows || rows.length === 0) return null
    const oldest = rows[rows.length - 1]
    const latest = rows[0]
    // Measure the period from the close before the first visible session when
    // we have it, so the first day's move counts too.
    const base = oldest.change != null ? oldest.close - oldest.change : oldest.open
    return {
      sessions: rows.length,
      latestClose: latest.close,
      change: latest.close - base,
      changePct: base !== 0 ? ((latest.close - base) / base) * 100 : 0,
      high: Math.max(...rows.map((r) => r.high)),
      low: Math.min(...rows.map((r) => r.low)),
      avgVolume: rows.reduce((s, r) => s + r.volume, 0) / rows.length,
    }
  }, [rows])

  function applyCustom() {
    if (!customFrom) return
    setApplied({ from: customFrom, to: customTo || today })
  }

  function pickPreset(id: PresetId) {
    setPreset(id)
    // Switching to Custom shows the prefilled window straight away instead of
    // an empty table waiting for Apply.
    if (id === 'custom' && !applied) setApplied({ from: customFrom, to: customTo || today })
  }

  function downloadCsv() {
    if (!rows || !symbol) return
    const header = 'date,open,high,low,close,change,change_pct,volume'
    const lines = [...rows]
      .reverse()
      .map((r) =>
        [
          dayKey(r.time),
          r.open,
          r.high,
          r.low,
          r.close,
          r.change?.toFixed(2) ?? '',
          r.changePct?.toFixed(2) ?? '',
          r.volume,
        ].join(','),
      )
    const blob = new Blob([`${header}\n${lines.join('\n')}\n`], { type: 'text/csv' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${symbol}-daily-${rows.length}sessions.csv`
    a.click()
    URL.revokeObjectURL(url)
  }

  const dateInputCls =
    'rounded-lg border border-slate-700 bg-slate-800 px-2.5 py-1.5 text-xs text-slate-100 [color-scheme:dark] focus:border-slate-500 focus:outline-none'

  return (
    <div className="space-y-4">
      <section className="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
        <div className="space-y-3">
          <SymbolSearch inputRef={searchRef} onPick={setSymbol} />
          <p className="px-1 text-[11px] text-slate-600">
            Search any NSE share to see its day-by-day prices.
          </p>

          {symbol && summary ? (
            <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
              <div className="flex items-baseline justify-between gap-2">
                <div className="text-lg font-semibold text-slate-100">{symbol}</div>
                <div className="text-right">
                  <div className="text-lg font-semibold tabular-nums text-slate-100">{inr(summary.latestClose)}</div>
                  <div className={`text-xs tabular-nums ${toneClass(summary.change)}`}>
                    {signedInr(summary.change)} ({pct(summary.changePct)}) over {summary.sessions} sessions
                  </div>
                </div>
              </div>
              <dl className="mt-3 grid grid-cols-3 gap-2 text-center text-xs">
                <div className="rounded-lg bg-slate-800/60 px-2 py-1.5">
                  <dt className="text-[10px] uppercase tracking-wide text-slate-500">High</dt>
                  <dd className="tabular-nums text-slate-200">{inr(summary.high)}</dd>
                </div>
                <div className="rounded-lg bg-slate-800/60 px-2 py-1.5">
                  <dt className="text-[10px] uppercase tracking-wide text-slate-500">Low</dt>
                  <dd className="tabular-nums text-slate-200">{inr(summary.low)}</dd>
                </div>
                <div className="rounded-lg bg-slate-800/60 px-2 py-1.5">
                  <dt className="text-[10px] uppercase tracking-wide text-slate-500">Avg vol</dt>
                  <dd className="tabular-nums text-slate-200">{compact(summary.avgVolume)}</dd>
                </div>
              </dl>
            </div>
          ) : null}
        </div>

        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex gap-1 rounded-xl border border-slate-800 bg-slate-900/60 p-1">
              {PRESETS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => pickPreset(p.id)}
                  className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${
                    preset === p.id ? 'bg-slate-100 text-slate-900' : 'text-slate-300 hover:bg-slate-800'
                  }`}
                >
                  {p.label}
                </button>
              ))}
              <button
                type="button"
                onClick={() => pickPreset('custom')}
                className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${
                  preset === 'custom' ? 'bg-slate-100 text-slate-900' : 'text-slate-300 hover:bg-slate-800'
                }`}
              >
                Custom
              </button>
            </div>

            {preset === 'custom' ? (
              <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400">
                <label className="flex items-center gap-1.5">
                  From
                  <input
                    type="date"
                    value={customFrom}
                    max={customTo || today}
                    onChange={(e) => setCustomFrom(e.target.value)}
                    className={dateInputCls}
                  />
                </label>
                <label className="flex items-center gap-1.5">
                  To
                  <input
                    type="date"
                    value={customTo}
                    min={customFrom}
                    max={today}
                    onChange={(e) => setCustomTo(e.target.value)}
                    className={dateInputCls}
                  />
                </label>
                <button
                  type="button"
                  onClick={applyCustom}
                  disabled={!customFrom || loading}
                  className="rounded-lg bg-slate-100 px-3 py-1.5 font-medium text-slate-900 transition-colors hover:bg-white disabled:opacity-50"
                >
                  Apply
                </button>
              </div>
            ) : null}

            {rows && rows.length > 0 ? (
              <button
                type="button"
                onClick={downloadCsv}
                className="ml-auto rounded-lg border border-slate-700 px-3 py-1.5 text-xs font-medium text-slate-300 transition-colors hover:bg-slate-800"
              >
                Download CSV
              </button>
            ) : null}
          </div>

          {error ? (
            <div className="rounded-xl border border-rose-900/60 bg-rose-950/40 px-4 py-3 text-sm text-rose-300">
              {error}
            </div>
          ) : null}

          {!symbol ? (
            <div className="rounded-xl border border-dashed border-slate-800 px-6 py-16 text-center text-sm text-slate-500">
              Search for a share to see its daily history.
            </div>
          ) : loading && !rows ? (
            <div className="rounded-xl border border-slate-800 px-6 py-16 text-center text-sm text-slate-500">
              Loading {symbol}'s daily history…
            </div>
          ) : rows && rows.length === 0 ? (
            <div className="rounded-xl border border-dashed border-slate-800 px-6 py-16 text-center text-sm text-slate-500">
              No trading sessions in this window — markets were closed, or the share wasn't listed yet.
            </div>
          ) : rows ? (
            <div className="overflow-hidden rounded-xl border border-slate-800">
              <div className="max-h-[34rem] overflow-auto">
                <table className="w-full min-w-[42rem] text-right text-sm tabular-nums">
                  <thead className="sticky top-0 bg-slate-900 text-[11px] uppercase tracking-wide text-slate-500">
                    <tr>
                      <th className="px-4 py-2.5 text-left font-medium">Date</th>
                      <th className="px-3 py-2.5 font-medium">Open</th>
                      <th className="px-3 py-2.5 font-medium">High</th>
                      <th className="px-3 py-2.5 font-medium">Low</th>
                      <th className="px-3 py-2.5 font-medium">Close</th>
                      <th className="px-3 py-2.5 font-medium">Change</th>
                      <th className="px-3 py-2.5 font-medium">Change %</th>
                      <th className="px-4 py-2.5 font-medium">Volume</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-800/70 bg-slate-950/40">
                    {rows.map((r) => {
                      const d = new Date(r.time)
                      return (
                        <tr key={r.time} className="transition-colors hover:bg-slate-900/60">
                          <td className="px-4 py-2 text-left">
                            <span className="text-slate-200">{displayDateFmt.format(d)}</span>
                            <span className="ml-2 text-[11px] text-slate-500">{weekdayFmt.format(d)}</span>
                          </td>
                          <td className="px-3 py-2 text-slate-300">{inr(r.open)}</td>
                          <td className="px-3 py-2 text-slate-300">{inr(r.high)}</td>
                          <td className="px-3 py-2 text-slate-300">{inr(r.low)}</td>
                          <td className="px-3 py-2 font-medium text-slate-100">{inr(r.close)}</td>
                          <td className={`px-3 py-2 ${r.change != null ? toneClass(r.change) : 'text-slate-500'}`}>
                            {r.change != null ? signedInr(r.change) : '—'}
                          </td>
                          <td className={`px-3 py-2 ${r.changePct != null ? toneClass(r.changePct) : 'text-slate-500'}`}>
                            {r.changePct != null ? pct(r.changePct) : '—'}
                          </td>
                          <td className="px-4 py-2 text-slate-400">{compact(r.volume)}</td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          ) : null}

          {rows && rows.length > 0 ? (
            <p className="px-1 text-[11px] text-slate-600">
              {rows.length} trading sessions · change is measured against the previous session's close · dates on the
              IST calendar
            </p>
          ) : null}
        </div>
      </section>
    </div>
  )
}
