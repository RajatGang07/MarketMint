import { useCallback, useEffect, useRef, useState } from 'react'

interface PollState<T> {
  data: T | undefined
  error: string | null
  refresh: () => void
}

/**
 * Fetches once on mount and then every `intervalMs`, and hands back a manual
 * `refresh` for after an action (placing an order, resetting the account).
 *
 * Polling is deliberate: prices move on the server and there is no websocket
 * feed yet, so the dashboard pulls rather than subscribes.
 */
export function usePoll<T>(fetcher: () => Promise<T>, intervalMs: number, deps: unknown[] = []): PollState<T> {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<string | null>(null)

  // Keep the latest fetcher in a ref so the interval isn't torn down and
  // recreated on every render.
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  const load = useCallback(async () => {
    try {
      setData(await fetcherRef.current())
      setError(null)
    } catch (err) {
      setError((err as Error).message)
    }
  }, [])

  useEffect(() => {
    let alive = true
    const tick = () => {
      if (alive) void load()
    }
    tick()
    const id = window.setInterval(tick, intervalMs)
    return () => {
      alive = false
      window.clearInterval(id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, intervalMs, ...deps])

  return { data, error, refresh: load }
}
