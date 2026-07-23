import { useEffect } from 'react'

export interface DetailRow {
  label: string
  value: string
  tone?: 'up' | 'down' | 'neutral'
}

/**
 * The one confirmation surface for anything that moves money. Replaces
 * window.confirm with something that can actually show the trade: entry,
 * stop, target, quantity and the rupees at stake.
 */
export function ConfirmDialog({
  open,
  title,
  rows = [],
  note,
  confirmLabel,
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean
  title: string
  rows?: DetailRow[]
  note?: string
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}) {
  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onCancel()
      if (e.key === 'Enter' && !busy) onConfirm()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, busy, onConfirm, onCancel])

  if (!open) return null

  const toneClass = (tone?: DetailRow['tone']) =>
    tone === 'up' ? 'text-emerald-400' : tone === 'down' ? 'text-rose-400' : 'text-slate-100'

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-slate-950/70 p-4 backdrop-blur-sm"
      onClick={onCancel}
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <div
        className="w-full max-w-sm rounded-2xl border border-slate-700 bg-slate-900 p-5 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="text-base font-semibold text-slate-100">{title}</h3>

        {rows.length > 0 ? (
          <dl className="mt-4 space-y-2">
            {rows.map((row) => (
              <div key={row.label} className="flex items-center justify-between text-sm">
                <dt className="text-slate-400">{row.label}</dt>
                <dd className={`font-medium tabular-nums ${toneClass(row.tone)}`}>{row.value}</dd>
              </div>
            ))}
          </dl>
        ) : null}

        {note ? <p className="mt-3 text-xs leading-relaxed text-slate-500">{note}</p> : null}

        <div className="mt-5 flex gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="flex-1 rounded-lg border border-slate-700 py-2 text-sm font-medium text-slate-300 transition-colors hover:bg-slate-800"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className={`flex-1 rounded-lg py-2 text-sm font-semibold text-white transition-colors disabled:opacity-60 ${
              danger ? 'bg-rose-600 hover:bg-rose-500' : 'bg-emerald-600 hover:bg-emerald-500'
            }`}
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
