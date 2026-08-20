import { useState } from 'react'

/**
 * Client-side pagination for blotter tables. The page survives the 5-second
 * account polls (state lives here, not in the data), and clamps rather than
 * jumping to page 1 when the list shrinks — an open order filling while you
 * are three pages deep should not teleport you.
 */
export function usePager<T>(items: T[], pageSize = 10) {
  const [rawPage, setPage] = useState(0)
  const pages = Math.max(1, Math.ceil(items.length / pageSize))
  const page = Math.min(rawPage, pages - 1)
  const slice = items.slice(page * pageSize, (page + 1) * pageSize)
  return { slice, page, pages, total: items.length, pageSize, setPage }
}

export function Pager({
  page,
  pages,
  total,
  pageSize,
  onPage,
}: {
  page: number
  pages: number
  total: number
  pageSize: number
  onPage: (page: number) => void
}) {
  if (pages <= 1) return null

  const from = page * pageSize + 1
  const to = Math.min(total, (page + 1) * pageSize)
  // A five-wide window of page numbers centred on the current page.
  const start = Math.max(0, Math.min(page - 2, pages - 5))
  const numbers = Array.from({ length: Math.min(5, pages) }, (_, i) => start + i)

  const btn =
    'rounded-md px-2 py-1 text-xs font-medium transition-colors disabled:cursor-default disabled:opacity-40'

  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-t border-slate-800 px-4 py-2">
      <span className="text-xs tabular-nums text-slate-500">
        {from}–{to} of {total}
      </span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={() => onPage(page - 1)}
          disabled={page === 0}
          className={`${btn} text-slate-400 hover:enabled:bg-slate-800 hover:enabled:text-slate-200`}
        >
          ‹ Prev
        </button>
        {numbers.map((n) => (
          <button
            key={n}
            type="button"
            onClick={() => onPage(n)}
            aria-current={n === page ? 'page' : undefined}
            className={`${btn} min-w-[1.75rem] tabular-nums ${
              n === page ? 'bg-slate-100 text-slate-900' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
            }`}
          >
            {n + 1}
          </button>
        ))}
        <button
          type="button"
          onClick={() => onPage(page + 1)}
          disabled={page === pages - 1}
          className={`${btn} text-slate-400 hover:enabled:bg-slate-800 hover:enabled:text-slate-200`}
        >
          Next ›
        </button>
      </div>
    </div>
  )
}
