const inrFmt = new Intl.NumberFormat('en-IN', {
  style: 'currency',
  currency: 'INR',
  maximumFractionDigits: 2,
})

const numFmt = new Intl.NumberFormat('en-IN', { maximumFractionDigits: 2 })
const compactFmt = new Intl.NumberFormat('en-IN', { notation: 'compact', maximumFractionDigits: 2 })

export const inr = (n: number): string => inrFmt.format(n ?? 0)
export const num = (n: number): string => numFmt.format(n ?? 0)
export const compact = (n: number): string => compactFmt.format(n ?? 0)
export const signedInr = (n: number): string => `${n >= 0 ? '+' : ''}${inr(n)}`
export const pct = (n: number): string => `${n >= 0 ? '+' : ''}${num(n)}%`

/** Tailwind text colour for a P&L or day-change figure. Zero reads as flat. */
export const toneClass = (n: number): string =>
  n > 0 ? 'text-emerald-400' : n < 0 ? 'text-rose-400' : 'text-slate-300'

const timeFmt = new Intl.DateTimeFormat('en-IN', { hour: '2-digit', minute: '2-digit' })
const dateFmt = new Intl.DateTimeFormat('en-IN', { day: '2-digit', month: 'short' })
const dateTimeFmt = new Intl.DateTimeFormat('en-IN', {
  day: '2-digit',
  month: 'short',
  hour: '2-digit',
  minute: '2-digit',
})

export const timeOf = (iso: string): string => timeFmt.format(new Date(iso))
export const dateOf = (iso: string): string => dateFmt.format(new Date(iso))
export const dateTimeOf = (iso: string): string => dateTimeFmt.format(new Date(iso))
