import { useState } from 'react'

import { api, session } from '../lib/api'
import { inr } from '../lib/format'

/**
 * Minimal-details entry: a username and a password. Signing up optionally
 * sets the starting equity (default ₹10,00,000) — every user gets their own
 * isolated paper account and session.
 */
export function AuthScreen({ onSignedIn }: { onSignedIn: (username: string) => void }) {
  const [mode, setMode] = useState<'login' | 'signup'>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [equity, setEquity] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const result =
        mode === 'login'
          ? await api.login(username, password)
          : await api.signup(username, password, equity ? Number(equity) : undefined)
      session.set(result.token)
      onSignedIn(result.username)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const equityPreview = equity ? Number(equity) : 1_000_000

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-semibold">MarketMint</h1>
          <p className="mt-1 text-sm text-slate-400">
            Paper trading on live NSE data — practice with virtual money, real prices.
          </p>
        </div>

        <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5">
          <div className="mb-4 flex gap-1 rounded-xl border border-slate-800 bg-slate-950/60 p-1">
            {(['login', 'signup'] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => {
                  setMode(m)
                  setError(null)
                }}
                className={`flex-1 rounded-lg py-2 text-sm font-medium transition-colors ${
                  mode === m ? 'bg-slate-100 text-slate-900' : 'text-slate-300 hover:bg-slate-800'
                }`}
              >
                {m === 'login' ? 'Sign in' : 'Create account'}
              </button>
            ))}
          </div>

          <form onSubmit={submit} className="space-y-3">
            <label className="block text-xs text-slate-400">
              Username
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                required
                minLength={3}
                placeholder="e.g. rajat"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder:text-slate-500 focus:border-slate-500 focus:outline-none"
              />
            </label>

            <label className="block text-xs text-slate-400">
              Password
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                required
                minLength={6}
                placeholder="at least 6 characters"
                className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder:text-slate-500 focus:border-slate-500 focus:outline-none"
              />
            </label>

            {mode === 'signup' ? (
              <label className="block text-xs text-slate-400">
                Starting equity (₹) — optional
                <input
                  type="number"
                  min={1000}
                  max={1000000000}
                  step={1000}
                  value={equity}
                  onChange={(e) => setEquity(e.target.value)}
                  placeholder="10,00,000 by default"
                  className="mt-1 w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-100 placeholder:text-slate-500 focus:border-slate-500 focus:outline-none"
                />
                <span className="mt-1 block text-[11px] text-slate-500">
                  You start with {inr(equityPreview)} of paper money. Changeable later (resets the account).
                </span>
              </label>
            ) : null}

            <button
              type="submit"
              disabled={busy}
              className="w-full rounded-lg bg-emerald-600 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-emerald-500 disabled:opacity-60"
            >
              {busy ? 'Working…' : mode === 'login' ? 'Sign in' : 'Create account & start trading'}
            </button>

            {error ? (
              <p className="rounded-lg bg-rose-500/15 px-3 py-2 text-xs text-rose-300">{error}</p>
            ) : null}
          </form>
        </div>

        <p className="mt-4 text-center text-[11px] text-slate-600">
          Paper trading only — no real money, no KYC, just a username and password.
        </p>
      </div>
    </div>
  )
}
