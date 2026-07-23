import { useEffect, useRef, useState } from 'react'

import { api, type Instrument } from '../lib/api'

/**
 * Type-ahead over the full NSE cash universe (~12k instruments loaded from
 * Groww's public instrument master).
 *
 * Requests are debounced and stamped with a sequence number, so a slow reply
 * for "REL" can never overwrite the results for "RELIANCE".
 */
export function SymbolSearch({
  onPick,
  inputRef,
}: {
  onPick: (symbol: string) => void
  inputRef?: React.RefObject<HTMLInputElement>
}) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Instrument[]>([])
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)

  const seq = useRef(0)
  const boxRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const q = query.trim()
    if (q.length < 2) {
      setResults([])
      return
    }
    const mine = ++seq.current
    const timer = window.setTimeout(async () => {
      try {
        const found = await api.searchInstruments(q)
        if (mine === seq.current) {
          setResults(found)
          setActive(0)
        }
      } catch {
        if (mine === seq.current) setResults([])
      }
    }, 180)
    return () => window.clearTimeout(timer)
  }, [query])

  // Clicking anywhere else dismisses the dropdown.
  useEffect(() => {
    function onDocClick(e: MouseEvent) {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDocClick)
    return () => document.removeEventListener('mousedown', onDocClick)
  }, [])

  function pick(symbol: string) {
    onPick(symbol)
    setQuery('')
    setResults([])
    setOpen(false)
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (!open || results.length === 0) {
      if (e.key === 'Enter' && query.trim()) pick(query.trim().toUpperCase())
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((i) => (i + 1) % results.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((i) => (i - 1 + results.length) % results.length)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      pick(results[active].trading_symbol)
    } else if (e.key === 'Escape') {
      setOpen(false)
    }
  }

  return (
    <div ref={boxRef} className="relative">
      <input
        ref={inputRef}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        placeholder="Search NSE — symbol or company"
        aria-label="Search instruments"
        autoComplete="off"
        className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder:text-slate-500 focus:border-slate-500 focus:outline-none"
      />

      {open && results.length > 0 ? (
        <ul className="absolute z-20 mt-1 max-h-72 w-full overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 shadow-xl">
          {results.map((inst, i) => (
            <li key={`${inst.exchange}-${inst.trading_symbol}-${inst.isin}`}>
              <button
                type="button"
                onMouseEnter={() => setActive(i)}
                onClick={() => pick(inst.trading_symbol)}
                className={`flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm transition-colors ${
                  i === active ? 'bg-slate-800' : 'hover:bg-slate-800/60'
                }`}
              >
                <span className="min-w-0">
                  <span className="block font-medium text-slate-100">{inst.trading_symbol}</span>
                  <span className="block truncate text-xs text-slate-500">
                    {inst.name || inst.isin}
                  </span>
                </span>
                <span className="shrink-0 rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-400">
                  {inst.exchange}
                </span>
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}
