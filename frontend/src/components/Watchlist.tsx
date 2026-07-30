import type { Quote } from '../lib/api'
import { num, pct, toneClass } from '../lib/format'

/** An automatically-pinned row: a holding or one of today's top picks. */
export interface AutoEntry {
  symbol: string
  tag: 'held' | 'pick'
}

const TAG_STYLE: Record<AutoEntry['tag'], string> = {
  held: 'bg-sky-500/15 text-sky-400',
  pick: 'bg-emerald-500/15 text-emerald-400',
}

/** Watchlist rows: live price plus the day move that gives it meaning. */
export function Watchlist({
  symbols,
  auto,
  quotes,
  selected,
  onSelect,
  onRemove,
}: {
  symbols: string[]
  auto: AutoEntry[]
  quotes: Record<string, Quote>
  selected: string
  onSelect: (symbol: string) => void
  onRemove: (symbol: string) => void
}) {
  return (
    <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="border-b border-slate-800 px-4 py-3 text-sm font-semibold">Watchlist</div>

      {symbols.length === 0 && auto.length === 0 ? (
        <p className="px-4 py-8 text-center text-sm text-slate-500">
          Search above to add an instrument.
        </p>
      ) : (
        <ul className="divide-y divide-slate-800/70">
          {symbols.map((s) => {
            const q = quotes[s]
            const active = s === selected
            return (
              <li key={s} className="group relative">
                <button
                  type="button"
                  onClick={() => onSelect(s)}
                  className={`flex w-full items-center justify-between gap-2 px-4 py-2.5 text-left text-sm transition-colors hover:bg-slate-800/60 ${
                    active ? 'bg-slate-800/80' : ''
                  }`}
                >
                  <span className="min-w-0">
                    <span className="block font-medium">{s}</span>
                    {q?.name ? (
                      <span className="block truncate text-xs text-slate-500">{q.name}</span>
                    ) : null}
                  </span>
                  <span className="shrink-0 pr-4 text-right tabular-nums">
                    <span className="block text-slate-200">{q?.ok ? num(q.last_price) : '—'}</span>
                    {q?.ok ? (
                      <span className={`block text-xs ${toneClass(q.change)}`}>
                        {pct(q.change_pct)}
                      </span>
                    ) : null}
                  </span>
                </button>

                <button
                  type="button"
                  onClick={() => onRemove(s)}
                  aria-label={`Remove ${s} from watchlist`}
                  className="absolute right-1.5 top-1/2 hidden -translate-y-1/2 rounded px-1.5 py-0.5 text-xs text-slate-500 hover:bg-slate-700 hover:text-slate-200 group-hover:block"
                >
                  ×
                </button>
              </li>
            )
          })}

          {auto.length > 0 ? (
            <li className="bg-slate-950/40 px-4 py-1.5 text-[10px] font-semibold uppercase tracking-wide text-slate-600">
              Auto · holdings &amp; top picks
            </li>
          ) : null}
          {auto.map(({ symbol, tag }) => {
            const q = quotes[symbol]
            const active = symbol === selected
            return (
              <li key={`auto-${symbol}`}>
                <button
                  type="button"
                  onClick={() => onSelect(symbol)}
                  className={`flex w-full items-center justify-between gap-2 px-4 py-2.5 text-left text-sm transition-colors hover:bg-slate-800/60 ${
                    active ? 'bg-slate-800/80' : ''
                  }`}
                >
                  <span className="min-w-0">
                    <span className="flex items-center gap-1.5 font-medium">
                      {symbol}
                      <span className={`rounded-full px-1.5 py-px text-[9px] font-semibold ${TAG_STYLE[tag]}`}>
                        {tag}
                      </span>
                    </span>
                    {q?.name ? (
                      <span className="block truncate text-xs text-slate-500">{q.name}</span>
                    ) : null}
                  </span>
                  <span className="shrink-0 text-right tabular-nums">
                    <span className="block text-slate-200">{q?.ok ? num(q.last_price) : '—'}</span>
                    {q?.ok ? (
                      <span className={`block text-xs ${toneClass(q.change)}`}>
                        {pct(q.change_pct)}
                      </span>
                    ) : null}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
