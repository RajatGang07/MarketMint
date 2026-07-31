/**
 * Indian equity charge bifurcation — what a real trade costs beyond the share
 * price. Pure math over one transparent rate table, so the numbers can be
 * audited line by line in the UI.
 *
 * Rates follow Groww's published schedule and NSE/statutory rates as of
 * mid-2025. Exchanges and brokers revise these; the tab shows every rate it
 * used so a stale number is visible, not hidden.
 */

export type Product = 'delivery' | 'intraday' | 'mtf'

// ---------------------------------------------------------------------------
// The rate table (every number the calculator uses, in one place)
// ---------------------------------------------------------------------------

export const RATES = {
  // Groww equity brokerage: 0.1% of order value capped at ₹20, floor ₹5,
  // charged per executed order on both sides.
  brokeragePct: 0.001,
  brokerageCap: 20,
  brokerageMin: 5,

  // Securities Transaction Tax.
  sttDeliveryPct: 0.001, // 0.1% on buy AND sell
  sttIntradaySellPct: 0.00025, // 0.025% on sell only

  // NSE transaction charges (capital market segment).
  exchangeTxnPct: 0.0000297, // 0.00297% both sides

  // SEBI turnover fee.
  sebiPct: 0.000001, // ₹10 per crore, both sides

  // Stamp duty (buy side only, on the buyer).
  stampDeliveryPct: 0.00015, // 0.015%
  stampIntradayPct: 0.00003, // 0.003%

  // GST on brokerage + exchange txn + SEBI fee.
  gstPct: 0.18,

  // Depository (DP) charge per scrip per day when shares leave the demat on
  // a delivery sell. Groww's flat rate, GST included.
  dpSellFlat: 18.5,

  // MTF: interest on the broker-funded portion, plus pledge/unpledge fees
  // per scrip (₹20 + GST each way).
  mtfInterestPctPa: 0.1499, // ~14.99% p.a., Groww's headline slab
  mtfFundedShare: 0.75, // broker typically funds up to ~75-80% (4x)
  pledgeFee: 23.6, // ₹20 + 18% GST, charged on pledge and again on unpledge
} as const

// ---------------------------------------------------------------------------
// Computation
// ---------------------------------------------------------------------------

export interface ChargeLine {
  name: string
  amount: number
  /** How the number was produced — shown under the line. */
  basis: string
}

export interface ChargeBreakdown {
  buyValue: number
  sellValue: number | null
  lines: ChargeLine[]
  totalCharges: number
  /** Price per share at which the round trip stops losing money. */
  breakEvenPrice: number
  /** Gross and net P&L; only when a sell price was supplied. */
  grossPnL: number | null
  netPnL: number | null
  /** Share of the buy value eaten by charges (round trip). */
  chargesPctOfTurnover: number
}

function brokerage(orderValue: number): number {
  if (orderValue <= 0) return 0
  return Math.max(RATES.brokerageMin, Math.min(RATES.brokerageCap, orderValue * RATES.brokeragePct))
}

const r2 = (n: number) => Math.round(n * 100) / 100

export interface ChargeInput {
  product: Product
  buyPrice: number
  quantity: number
  /** Optional: completes the round trip; break-even is computed regardless. */
  sellPrice?: number | null
  /** MTF only: days the funded position is held. */
  mtfDays?: number
  /** MTF only: annual interest rate override (fraction, e.g. 0.15). */
  mtfRatePa?: number
  /** MTF only: broker-funded share of the buy value (fraction). */
  mtfFunded?: number
}

export function computeCharges(input: ChargeInput): ChargeBreakdown {
  const { product, buyPrice, quantity } = input
  const qty = Math.max(0, Math.floor(quantity))
  const buyValue = buyPrice * qty
  const sellPrice = input.sellPrice && input.sellPrice > 0 ? input.sellPrice : null
  // Charges that scale with the sell side use the actual sell value when
  // given, otherwise the buy value as a stand-in so the estimate is complete.
  const sellValue = sellPrice ? sellPrice * qty : null
  const sellBasis = sellValue ?? buyValue

  const lines: ChargeLine[] = []
  const add = (name: string, amount: number, basis: string) => {
    if (amount > 0) lines.push({ name, amount: r2(amount), basis })
  }

  // --- Brokerage -------------------------------------------------------
  const brokerageBuy = brokerage(buyValue)
  const brokerageSell = brokerage(sellBasis)
  add('Brokerage (buy)', brokerageBuy, '0.1% capped at ₹20, min ₹5, per order')
  add('Brokerage (sell)', brokerageSell, '0.1% capped at ₹20, min ₹5, per order')

  // --- STT -------------------------------------------------------------
  if (product === 'intraday') {
    add('STT (sell)', sellBasis * RATES.sttIntradaySellPct, '0.025% of sell value — intraday pays STT on the sell only')
  } else {
    add('STT (buy)', buyValue * RATES.sttDeliveryPct, '0.1% of buy value')
    add('STT (sell)', sellBasis * RATES.sttDeliveryPct, '0.1% of sell value')
  }

  // --- Exchange + SEBI -------------------------------------------------
  const txn = (buyValue + sellBasis) * RATES.exchangeTxnPct
  add('Exchange transaction charges', txn, 'NSE 0.00297% of turnover, both sides')
  const sebi = (buyValue + sellBasis) * RATES.sebiPct
  add('SEBI turnover fee', sebi, '₹10 per crore of turnover, both sides')

  // --- Stamp duty (buy only) -------------------------------------------
  const stampPct = product === 'intraday' ? RATES.stampIntradayPct : RATES.stampDeliveryPct
  add('Stamp duty (buy)', buyValue * stampPct, `${product === 'intraday' ? '0.003' : '0.015'}% of buy value, buyer pays`)

  // --- GST -------------------------------------------------------------
  const gst = (brokerageBuy + brokerageSell + txn + sebi) * RATES.gstPct
  add('GST', gst, '18% on brokerage + exchange charges + SEBI fee')

  // --- DP charge (delivery/MTF sell) -----------------------------------
  if (product !== 'intraday') {
    add('DP charge (sell)', RATES.dpSellFlat, 'flat per scrip per day when demat shares are sold, GST included')
  }

  // --- MTF extras ------------------------------------------------------
  if (product === 'mtf') {
    const funded = buyValue * (input.mtfFunded ?? RATES.mtfFundedShare)
    const rate = input.mtfRatePa ?? RATES.mtfInterestPctPa
    const days = Math.max(1, input.mtfDays ?? 1)
    add(
      `MTF interest (${days} day${days > 1 ? 's' : ''})`,
      funded * (rate / 365) * days,
      `${(rate * 100).toFixed(2)}% p.a. on the funded ₹${Math.round(funded).toLocaleString('en-IN')} (${Math.round(
        (input.mtfFunded ?? RATES.mtfFundedShare) * 100,
      )}% of buy value)`,
    )
    add('Pledge + unpledge', RATES.pledgeFee * 2, '₹20 + GST per scrip, charged on pledge and again on unpledge')
  }

  const totalCharges = r2(lines.reduce((s, l) => s + l.amount, 0))
  const grossPnL = sellValue !== null ? r2(sellValue - buyValue) : null
  const netPnL = grossPnL !== null ? r2(grossPnL - totalCharges) : null

  return {
    buyValue: r2(buyValue),
    sellValue: sellValue !== null ? r2(sellValue) : null,
    lines,
    totalCharges,
    breakEvenPrice: qty > 0 ? r2(buyPrice + totalCharges / qty) : 0,
    grossPnL,
    netPnL,
    chargesPctOfTurnover: buyValue > 0 ? r2((totalCharges / (buyValue + sellBasis)) * 100) : 0,
  }
}
