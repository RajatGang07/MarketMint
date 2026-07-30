import { useCallback, useEffect, useRef, useState } from 'react'

import type { AutopilotLogEntry, Trade } from './api'

const ENABLED_KEY = 'paper-trading.notify'
const LAST_TRADE_KEY = 'paper-trading.notify.last-trade'
const LAST_PILOT_KEY = 'paper-trading.notify.last-pilot'

/**
 * Browser notifications for fills and autopilot errors.
 *
 * Fills are the ground truth — one notification per executed trade covers
 * manual orders, autopilot entries/exits AND bracket/trailing exits fired by
 * the background matcher. Autopilot SKIPs are deliberately silent (noise);
 * its ERRORs are not.
 *
 * Limitation stated honestly: the browser must be running (tab may be in the
 * background) — closed browser means no notification. A Telegram bridge would
 * lift that; it needs a bot token.
 */
export function useTradeNotifications(trades: Trade[] | undefined, pilotLog: AutopilotLogEntry[] | undefined) {
  const [enabled, setEnabled] = useState<boolean>(
    () => localStorage.getItem(ENABLED_KEY) === 'on' && typeof Notification !== 'undefined' && Notification.permission === 'granted',
  )
  const [denied, setDenied] = useState<boolean>(
    () => typeof Notification !== 'undefined' && Notification.permission === 'denied',
  )
  // Baselines survive reloads so history is never replayed as a flood.
  const lastTrade = useRef<number>(Number(localStorage.getItem(LAST_TRADE_KEY) ?? 0))
  const lastPilot = useRef<number>(Number(localStorage.getItem(LAST_PILOT_KEY) ?? 0))

  const toggle = useCallback(async () => {
    if (typeof Notification === 'undefined') return
    if (enabled) {
      setEnabled(false)
      localStorage.setItem(ENABLED_KEY, 'off')
      return
    }
    const perm = await Notification.requestPermission()
    if (perm === 'granted') {
      setEnabled(true)
      setDenied(false)
      localStorage.setItem(ENABLED_KEY, 'on')
      new Notification('MarketMint notifications on', {
        body: 'You will hear about every fill and autopilot error while the browser is open.',
      })
    } else {
      setDenied(perm === 'denied')
    }
  }, [enabled])

  useEffect(() => {
    if (!trades?.length) return
    const maxId = Math.max(...trades.map((t) => t.id))
    // First sighting just sets the baseline — no replay of old history.
    if (lastTrade.current === 0) {
      lastTrade.current = maxId
      localStorage.setItem(LAST_TRADE_KEY, String(maxId))
      return
    }
    if (maxId <= lastTrade.current) return
    if (enabled) {
      for (const t of trades.filter((t) => t.id > lastTrade.current).slice(0, 5)) {
        const verb = t.transaction_type === 'BUY' ? 'Bought' : 'Sold'
        new Notification(`${verb} ${t.quantity} ${t.trading_symbol}`, {
          body: `@ ₹${t.price}${t.realized_pnl ? ` · realised P&L ₹${t.realized_pnl.toFixed(0)}` : ''}`,
          tag: `trade-${t.id}`,
        })
      }
    }
    lastTrade.current = maxId
    localStorage.setItem(LAST_TRADE_KEY, String(maxId))
  }, [trades, enabled])

  useEffect(() => {
    if (!pilotLog?.length) return
    const maxId = Math.max(...pilotLog.map((e) => e.id))
    if (lastPilot.current === 0) {
      lastPilot.current = maxId
      localStorage.setItem(LAST_PILOT_KEY, String(maxId))
      return
    }
    if (maxId <= lastPilot.current) return
    if (enabled) {
      for (const e of pilotLog.filter((e) => e.id > lastPilot.current && e.action === 'ERROR').slice(0, 3)) {
        new Notification(`Autopilot error${e.symbol ? `: ${e.symbol}` : ''}`, {
          body: e.detail.slice(0, 140),
          tag: `pilot-${e.id}`,
        })
      }
    }
    lastPilot.current = maxId
    localStorage.setItem(LAST_PILOT_KEY, String(maxId))
  }, [pilotLog, enabled])

  return { enabled, denied, toggle, supported: typeof Notification !== 'undefined' }
}
