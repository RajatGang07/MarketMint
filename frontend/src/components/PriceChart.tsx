import { useMemo, useRef, useState } from 'react'

import type { Candle, ChartRange } from '../lib/api'
import { compact, dateOf, dateTimeOf, inr, num, timeOf } from '../lib/format'

const RANGES: ChartRange[] = ['1d', '5d', '1mo', '3mo', '1y']
const RANGE_LABEL: Record<ChartRange, string> = {
  '1d': '1D',
  '5d': '5D',
  '1mo': '1M',
  '3mo': '3M',
  '1y': '1Y',
}

// Viewbox units. The SVG scales to its container, so these are a coordinate
// system rather than pixels.
const W = 800
const H = 260
const PAD = { top: 12, right: 56, bottom: 24, left: 8 }
const PLOT_W = W - PAD.left - PAD.right
const PLOT_H = H - PAD.top - PAD.bottom

interface Props {
  symbol: string
  candles: Candle[]
  /** Previous close — the reference the day's move is measured against. */
  previousClose?: number
  range: ChartRange
  onRangeChange: (range: ChartRange) => void
  loading?: boolean
  error?: string | null
}

/**
 * Intraday/historical price line.
 *
 * One series, so there is no legend — the header names it. The line is coloured
 * by direction over the window (up/down), which is polarity rather than
 * identity, and the dashed reference line makes that comparison explicit
 * instead of asking the reader to infer it from a truncated y-axis.
 */
export function PriceChart({
  symbol,
  candles,
  previousClose,
  range,
  onRangeChange,
  loading,
  error,
}: Props) {
  const svgRef = useRef<SVGSVGElement>(null)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)

  const geom = useMemo(() => {
    const points = candles.filter((c) => Number.isFinite(c.close) && c.close > 0)
    if (points.length < 2) return null

    const closes = points.map((c) => c.close)
    // Include the reference line in the domain so it can never fall outside
    // the plot and silently disappear.
    const domain = previousClose && range === '1d' ? [...closes, previousClose] : closes

    const rawMin = Math.min(...domain)
    const rawMax = Math.max(...domain)
    // A flat series would collapse to a zero-height scale; give it breathing room.
    const spread = rawMax - rawMin || Math.max(rawMax * 0.01, 1)
    const min = rawMin - spread * 0.08
    const max = rawMax + spread * 0.08

    const x = (i: number) => PAD.left + (i / (points.length - 1)) * PLOT_W
    const y = (v: number) => PAD.top + (1 - (v - min) / (max - min)) * PLOT_H

    const line = points.map((c, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(2)} ${y(c.close).toFixed(2)}`).join(' ')
    const area = `${line} L ${x(points.length - 1).toFixed(2)} ${PAD.top + PLOT_H} L ${x(0).toFixed(2)} ${PAD.top + PLOT_H} Z`

    const first = points[0].close
    const last = points[points.length - 1].close
    const baseline = range === '1d' && previousClose ? previousClose : first

    return { points, x, y, line, area, min, max, last, baseline, up: last >= baseline }
  }, [candles, previousClose, range])

  // Nearest-point lookup: the pointer maps to a data index, not to a mark, so
  // the whole plot area is a hit target rather than the 2px line itself.
  function onPointerMove(e: React.PointerEvent<SVGSVGElement>) {
    if (!geom || !svgRef.current) return
    const rect = svgRef.current.getBoundingClientRect()
    const ratio = (e.clientX - rect.left) / rect.width
    const svgX = ratio * W
    const t = (svgX - PAD.left) / PLOT_W
    const idx = Math.round(t * (geom.points.length - 1))
    setHoverIndex(Math.min(geom.points.length - 1, Math.max(0, idx)))
  }

  const hovered = geom && hoverIndex != null ? geom.points[hoverIndex] : null
  const stroke = geom?.up ? '#34d399' : '#fb7185'
  const gradientId = `price-fill-${geom?.up ? 'up' : 'down'}`

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-4 py-2.5">
        <span className="text-sm font-semibold">{symbol} price</span>
        <div className="flex gap-1" role="group" aria-label="Chart range">
          {RANGES.map((r) => (
            <button
              key={r}
              type="button"
              onClick={() => onRangeChange(r)}
              aria-pressed={r === range}
              className={`rounded px-2 py-1 text-xs font-medium transition-colors ${
                r === range ? 'bg-slate-700 text-slate-100' : 'text-slate-400 hover:bg-slate-800'
              }`}
            >
              {RANGE_LABEL[r]}
            </button>
          ))}
        </div>
      </div>

      <div className="relative px-2 py-2">
        {error ? (
          <div className="flex h-[260px] items-center justify-center px-4 text-center text-sm text-rose-300">
            {error}
          </div>
        ) : !geom ? (
          <div className="flex h-[260px] items-center justify-center text-sm text-slate-500">
            {loading ? 'Loading chart…' : 'No price history for this range.'}
          </div>
        ) : (
          <>
            <svg
              ref={svgRef}
              viewBox={`0 0 ${W} ${H}`}
              className="h-[260px] w-full touch-none"
              onPointerMove={onPointerMove}
              onPointerLeave={() => setHoverIndex(null)}
              role="img"
              aria-label={`${symbol} price over ${RANGE_LABEL[range]}, last ${inr(geom.last)}`}
            >
              <defs>
                <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={stroke} stopOpacity="0.22" />
                  <stop offset="100%" stopColor={stroke} stopOpacity="0" />
                </linearGradient>
              </defs>

              {/* Recessive gridlines: four steps, labelled on the right. */}
              {[0, 0.25, 0.5, 0.75, 1].map((t) => {
                const value = geom.max - t * (geom.max - geom.min)
                const y = PAD.top + t * PLOT_H
                return (
                  <g key={t}>
                    <line
                      x1={PAD.left}
                      x2={PAD.left + PLOT_W}
                      y1={y}
                      y2={y}
                      stroke="#1e293b"
                      strokeWidth="1"
                    />
                    <text
                      x={PAD.left + PLOT_W + 8}
                      y={y + 3.5}
                      fill="#64748b"
                      fontSize="10"
                      fontFamily="ui-sans-serif, system-ui"
                    >
                      {num(value)}
                    </text>
                  </g>
                )
              })}

              {/* Previous close, so the day's move is read against something. */}
              {range === '1d' && previousClose ? (
                <g>
                  <line
                    x1={PAD.left}
                    x2={PAD.left + PLOT_W}
                    y1={geom.y(previousClose)}
                    y2={geom.y(previousClose)}
                    stroke="#475569"
                    strokeWidth="1"
                    strokeDasharray="4 4"
                  />
                  <text
                    x={PAD.left + PLOT_W + 8}
                    y={geom.y(previousClose) - 4}
                    fill="#94a3b8"
                    fontSize="9"
                    fontFamily="ui-sans-serif, system-ui"
                  >
                    prev
                  </text>
                </g>
              ) : null}

              <path d={geom.area} fill={`url(#${gradientId})`} />
              <path
                d={geom.line}
                fill="none"
                stroke={stroke}
                strokeWidth="2"
                strokeLinejoin="round"
                strokeLinecap="round"
              />

              {/* Direct label on the latest point only — never one per point. */}
              <circle cx={geom.x(geom.points.length - 1)} cy={geom.y(geom.last)} r="3.5" fill={stroke} />

              {hovered ? (
                <g pointerEvents="none">
                  <line
                    x1={geom.x(hoverIndex!)}
                    x2={geom.x(hoverIndex!)}
                    y1={PAD.top}
                    y2={PAD.top + PLOT_H}
                    stroke="#64748b"
                    strokeWidth="1"
                  />
                  {/* 2px surface ring keeps the marker legible over the line. */}
                  <circle
                    cx={geom.x(hoverIndex!)}
                    cy={geom.y(hovered.close)}
                    r="4.5"
                    fill={stroke}
                    stroke="#0f172a"
                    strokeWidth="2"
                  />
                </g>
              ) : null}

              {/* X labels: first, middle and last only, to stay uncluttered. */}
              {[0, Math.floor(geom.points.length / 2), geom.points.length - 1].map((i, n) => (
                <text
                  key={i}
                  x={geom.x(i)}
                  y={H - 8}
                  fill="#64748b"
                  fontSize="10"
                  fontFamily="ui-sans-serif, system-ui"
                  textAnchor={n === 0 ? 'start' : n === 2 ? 'end' : 'middle'}
                >
                  {range === '1d' ? timeOf(geom.points[i].time) : dateOf(geom.points[i].time)}
                </text>
              ))}
            </svg>

            {hovered ? (
              <div className="pointer-events-none absolute left-3 top-3 rounded-lg border border-slate-700 bg-slate-900/95 px-3 py-2 text-xs shadow-lg">
                <div className="mb-1 text-slate-400">
                  {range === '1d' ? timeOf(hovered.time) : dateTimeOf(hovered.time)}
                </div>
                <div className="grid grid-cols-2 gap-x-4 gap-y-0.5 tabular-nums">
                  <span className="text-slate-500">Open</span>
                  <span className="text-right text-slate-200">{num(hovered.open)}</span>
                  <span className="text-slate-500">High</span>
                  <span className="text-right text-slate-200">{num(hovered.high)}</span>
                  <span className="text-slate-500">Low</span>
                  <span className="text-right text-slate-200">{num(hovered.low)}</span>
                  <span className="text-slate-500">Close</span>
                  <span className="text-right font-medium text-slate-100">{num(hovered.close)}</span>
                  {hovered.volume > 0 ? (
                    <>
                      <span className="text-slate-500">Volume</span>
                      <span className="text-right text-slate-200">{compact(hovered.volume)}</span>
                    </>
                  ) : null}
                </div>
              </div>
            ) : null}
          </>
        )}
      </div>
    </div>
  )
}
