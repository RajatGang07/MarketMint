import { useEffect, useState } from 'react'

import { api, type AutopilotLogEntry, type AutopilotSettings } from '../lib/api'
import { dateTimeOf } from '../lib/format'
import { useToast } from '../lib/toast'
import { usePoll } from '../lib/usePoll'

const ACTION_STYLE: Record<string, string> = {
  BUY: 'bg-emerald-500/15 text-emerald-400',
  SELL: 'bg-rose-500/15 text-rose-400',
  SKIP: 'bg-slate-600/30 text-slate-400',
  ERROR: 'bg-amber-500/15 text-amber-400',
}

/**
 * The Autopilot tab: switch on automated trading for this paper account,
 * bound it with a few limits, and audit every decision the robot makes.
 */
export function Autopilot({ active, onTraded }: { active: boolean; onTraded: () => void }) {
  const toast = useToast()
  const [settings, setSettings] = useState<AutopilotSettings | null>(null)
  const [saving, setSaving] = useState(false)

  // Form state mirrors settings but is edited freely until Save.
  const [maxPositions, setMaxPositions] = useState('5')
  const [maxCapital, setMaxCapital] = useState('200000')
  const [trailStops, setTrailStops] = useState(true)

  useEffect(() => {
    if (!active || settings) return
    api
      .autopilot()
      .then((s) => {
        setSettings(s)
        setMaxPositions(String(s.max_positions))
        setMaxCapital(String(s.max_capital_per_trade))
        setTrailStops(s.trail_stops)
      })
      .catch((err: Error) => toast.push('error', err.message))
  }, [active, settings, toast])

  const log = usePoll<AutopilotLogEntry[]>(
    () => (active ? api.autopilotLog() : Promise.resolve([] as AutopilotLogEntry[])),
    15_000,
    [active],
  )

  async function save(next: Partial<AutopilotSettings>) {
    if (!settings) return
    const body: AutopilotSettings = {
      enabled: next.enabled ?? settings.enabled,
      max_positions: Number(maxPositions) || settings.max_positions,
      max_capital_per_trade: Number(maxCapital) || settings.max_capital_per_trade,
      trail_stops: trailStops,
    }
    setSaving(true)
    try {
      const saved = await api.saveAutopilot(body)
      setSettings(saved)
      toast.push(
        'success',
        saved.enabled
          ? 'Autopilot on — first pass is running now; decisions appear in the log below.'
          : 'Autopilot off. Resting bracket orders keep protecting open positions.',
      )
      onTraded()
      setTimeout(() => log.refresh(), 4000)
    } catch (err) {
      toast.push('error', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  if (!settings) {
    return <div className="rounded-xl border border-slate-800 px-6 py-16 text-center text-sm text-slate-500">Loading…</div>
  }

  return (
    <div className="space-y-4">
      <section className="grid gap-4 lg:grid-cols-[24rem_minmax(0,1fr)]">
        <div className="space-y-3">
          {/* Master switch */}
          <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-slate-100">Autopilot</div>
                <div className="mt-0.5 text-xs text-slate-400">
                  Trades this paper account automatically from the signals board.
                </div>
              </div>
              <button
                type="button"
                disabled={saving}
                onClick={() => save({ enabled: !settings.enabled })}
                aria-pressed={settings.enabled}
                className={`relative h-7 w-13 shrink-0 rounded-full transition-colors ${
                  settings.enabled ? 'bg-emerald-500' : 'bg-slate-700'
                } ${saving ? 'opacity-50' : ''}`}
                style={{ width: '3.25rem' }}
              >
                <span
                  className={`absolute top-0.5 h-6 w-6 rounded-full bg-white transition-all ${
                    settings.enabled ? 'left-6' : 'left-0.5'
                  }`}
                />
              </button>
            </div>
            <div
              className={`mt-3 rounded-lg px-3 py-2 text-xs ${
                settings.enabled ? 'bg-emerald-500/10 text-emerald-300' : 'bg-slate-800/60 text-slate-400'
              }`}
            >
              {settings.enabled
                ? 'ON — buys top-ranked momentum names with stop/target brackets, sells holdings whose trend has broken. Every decision is logged below.'
                : 'OFF — the board still recommends; nothing trades without you.'}
            </div>
          </div>

          {/* Limits */}
          <div className="space-y-3 rounded-xl border border-slate-800 bg-slate-900/60 p-4">
            <div className="text-[11px] font-semibold uppercase tracking-wide text-slate-500">Limits</div>

            <label className="block text-xs text-slate-400">
              Max concurrent positions
              <input
                type="number"
                min={1}
                max={20}
                value={maxPositions}
                onChange={(e) => setMaxPositions(e.target.value)}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-slate-500"
              />
            </label>

            <label className="block text-xs text-slate-400">
              Max capital per trade (₹)
              <input
                type="number"
                min={1000}
                step={10000}
                value={maxCapital}
                onChange={(e) => setMaxCapital(e.target.value)}
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-slate-500"
              />
            </label>

            <label className="flex items-center gap-2 text-xs text-slate-300">
              <input
                type="checkbox"
                checked={trailStops}
                onChange={(e) => setTrailStops(e.target.checked)}
                className="h-4 w-4 rounded border-slate-600 bg-slate-950"
              />
              Trail stops as price runs (locks in gains)
            </label>

            <button
              type="button"
              disabled={saving}
              onClick={() => save({})}
              className="w-full rounded-lg bg-slate-100 px-3 py-2 text-sm font-medium text-slate-900 transition-opacity hover:opacity-90 disabled:opacity-50"
            >
              {saving ? 'Saving…' : 'Save limits'}
            </button>
          </div>

          {/* The rules, stated plainly */}
          <div className="rounded-xl border border-slate-800/70 bg-slate-900/40 p-4 text-xs text-slate-400">
            <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-slate-500">The rules</div>
            <ul className="list-disc space-y-1 pl-4">
              <li>
                <span className="text-emerald-400">Buys</span> when a share ranks in the momentum top-10 with a
                risk-sized plan — entry, stop-loss and target placed together.
              </li>
              <li>
                <span className="text-rose-400">Sells</span> a holding when its rank collapses, RSI hits blow-off, or
                it is losing with no stop resting.
              </li>
              <li>One trade per share per day; skips are logged with reasons.</li>
              <li>Runs every ~10 minutes. Paper money only — no real orders exist anywhere in this platform.</li>
            </ul>
          </div>
        </div>

        {/* Decision log */}
        <div className="rounded-xl border border-slate-800 bg-slate-900/60">
          <div className="flex items-center justify-between border-b border-slate-800/70 px-4 py-3">
            <div className="text-sm font-semibold text-slate-100">Decision log</div>
            <button
              type="button"
              onClick={() => log.refresh()}
              className="rounded-lg border border-slate-700 px-2.5 py-1 text-xs text-slate-300 hover:bg-slate-800"
            >
              Refresh
            </button>
          </div>

          {log.error ? <div className="px-4 py-3 text-xs text-rose-300">{log.error}</div> : null}

          {(log.data?.length ?? 0) === 0 ? (
            <div className="px-6 py-16 text-center text-sm text-slate-500">
              {settings.enabled
                ? 'No decisions yet — the first pass runs shortly after enabling, then every ~10 minutes.'
                : 'Nothing yet. Switch Autopilot on and every buy, sell and skip will be explained here.'}
            </div>
          ) : (
            <ul className="divide-y divide-slate-800/60">
              {log.data!.map((e) => (
                <li key={e.id} className="flex items-start gap-3 px-4 py-3">
                  <span
                    className={`mt-0.5 inline-flex w-14 shrink-0 justify-center rounded-full px-2 py-0.5 text-[11px] font-semibold ${
                      ACTION_STYLE[e.action] ?? ACTION_STYLE.SKIP
                    }`}
                  >
                    {e.action}
                  </span>
                  <div className="min-w-0 flex-1 text-xs">
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      {e.symbol ? <span className="font-semibold text-slate-100">{e.symbol}</span> : null}
                      <span className="text-slate-500">{dateTimeOf(e.at)}</span>
                      {e.order_ref ? <span className="text-slate-600">#{e.order_ref}</span> : null}
                    </div>
                    <p className="mt-0.5 leading-snug text-slate-300">{e.detail}</p>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </div>
  )
}
