import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react'

type Kind = 'success' | 'error' | 'info'

interface Toast {
  id: number
  kind: Kind
  text: string
}

interface ToastAPI {
  push: (kind: Kind, text: string) => void
}

const ToastContext = createContext<ToastAPI>({ push: () => undefined })

/** useToast().push('success', 'Order filled') — feedback without layout shift. */
export function useToast(): ToastAPI {
  return useContext(ToastContext)
}

const KIND_STYLE: Record<Kind, string> = {
  success: 'border-emerald-700/60 bg-emerald-950/90 text-emerald-200',
  error: 'border-rose-700/60 bg-rose-950/90 text-rose-200',
  info: 'border-slate-700 bg-slate-900/95 text-slate-200',
}

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const nextId = useRef(1)

  const push = useCallback((kind: Kind, text: string) => {
    const id = nextId.current++
    setToasts((t) => [...t, { id, kind, text }])
    // Errors linger a little longer — they usually need reading.
    window.setTimeout(
      () => setToasts((t) => t.filter((x) => x.id !== id)),
      kind === 'error' ? 6000 : 3500,
    )
  }, [])

  const api = useMemo(() => ({ push }), [push])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            role="status"
            className={`pointer-events-auto rounded-lg border px-3 py-2 text-sm shadow-xl backdrop-blur ${KIND_STYLE[t.kind]}`}
          >
            {t.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
