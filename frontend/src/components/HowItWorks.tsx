/**
 * The manual: every feature, what drives it, and where its limits are. The
 * platform's credibility rests on explaining its own logic — including the
 * parts that are proxies, caveats and known weaknesses.
 */

function Section({ title, tag, children }: { title: string; tag?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-slate-800 bg-slate-900/60 p-5">
      <div className="mb-3 flex items-baseline gap-2">
        <h2 className="text-base font-semibold text-slate-100">{title}</h2>
        {tag ? (
          <span className="rounded-full bg-slate-700/40 px-2 py-0.5 text-[10px] font-medium text-slate-400">{tag}</span>
        ) : null}
      </div>
      <div className="space-y-2 text-sm leading-relaxed text-slate-300">{children}</div>
    </section>
  )
}

function Point({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <p>
      <span className="font-medium text-slate-100">{k}</span> <span className="text-slate-400">— {children}</span>
    </p>
  )
}

export function HowItWorks() {
  return (
    <div className="mx-auto max-w-4xl space-y-4">
      <div className="rounded-xl border border-slate-800/70 bg-slate-900/40 px-5 py-4 text-sm text-slate-400">
        MarketMint is a <span className="text-slate-200">paper-trading platform</span>: real market prices, a virtual
        cash account, and orders that never reach an exchange. Everything below explains what each feature does and
        the logic behind it — including the honest limits. Nothing here is investment advice.
      </div>

      <Section title="Market data" tag="prices">
        <Point k="Fallback chain">
          prices come from the first working source in <em>Groww → Yahoo Finance → simulator</em>. A failing source is
          skipped for two minutes, then re-probed. The header chip always shows which source is live — because trading
          simulated prices without knowing it would be the most misleading thing this app could do.
        </Point>
        <Point k="Instruments">
          the search covers ~12,000 NSE cash instruments from Groww&apos;s public instrument master, cached for 12 hours.
        </Point>
      </Section>

      <Section title="Paper trading engine" tag="orders">
        <Point k="Order types">
          MARKET (fills at the live price), LIMIT (rests until the price crosses), SL stop-market (arms at a trigger,
          then fills at market — modelling the gap risk a real stop carries).
        </Point>
        <Point k="Brackets">
          a BUY can carry its exit plan: a stop-loss and optionally a target. Once the buy fills, the exits are placed
          server-side as an OCO pair — when one fills, the other cancels. A background matcher checks resting orders
          every few seconds, so exits fire even with the dashboard closed.
        </Point>
        <Point k="Trailing stops">
          the stop&apos;s trigger ratchets up as the price makes new highs (high − trail distance), locking in profit while
          never moving down.
        </Point>
        <Point k="Honest gap">paper fills pay no brokerage, STT or slippage — live results would be lower.</Point>
      </Section>

      <Section title="Ideas & Signals" tag="recommendations">
        <Point k="The model">
          a transparent momentum composite over ~1 year of daily bars: 3-month momentum (30%), 1-month momentum (20%),
          trend persistence (15%), proximity to the 60-day high (15%), volume expansion (10%), RSI band (10%). Stocks
          under ₹50 or below ₹5cr daily turnover are screened out; RSI &gt; 80 is never chased.
        </Point>
        <Point k="Why momentum">
          over 1–2 month horizons, cross-sectional momentum is the one equity anomaly with enough published evidence to
          lean on. The edge is modest and the tab shows its own walk-forward backtest — the honesty box — so you see
          exactly how weak or strong it currently is.
        </Point>
        <Point k="Signals board">
          one verdict per stock: BUY (top-10 rank and a risk-sized plan fits), SELL (holdings only: rank collapsed, RSI
          blow-off, or losing with no stop resting), WATCH, HOLD. Every verdict lists its reasons.
        </Point>
        <Point k="Intraday scanner">
          opening-range-breakout longs on 5-minute bars: entry when a bar closes above the first-15-minute high before
          14:30 with 1.5× volume and price above VWAP; stop at the OR low or 1.5×ATR; 2R target; stop trails after 1R;
          everything squares off by 15:15. The identical rule is backtested over ~20 sessions and shown.
        </Point>
        <Point k="Position sizing">
          plans size so the loss at the stop lands in a fixed risk band (default ₹20–30k) — volatility (ATR) sets the
          stop distance, the risk budget sets the quantity. Sizing and stops carry the risk; the signal only picks.
        </Point>
      </Section>

      <Section title="Forecast" tag="predictions">
        <Point k="What it is">
          directional leans for one share across four horizons — not price targets. Probabilities are deliberately
          capped near 50% because short-horizon direction is mostly noise.
        </Point>
        <Point k="Drivers">
          technical strength (RSI, momentum, moving averages, trend persistence), an intraday flow proxy (price vs
          VWAP, share of volume on rising bars, close location — a proxy because no order-book depth feed is wired),
          news sentiment, and the market regime (NIFTY vs its 50-day average, via the NIFTYBEES ETF). Each driver shows
          its score, weight and reasoning.
        </Point>
        <Point k="News sentiment">
          recent headlines from Google News, scored by Claude when an API key is configured, otherwise by a keyword
          lexicon. The method used is always labelled.
        </Point>
        <Point k="The seconds row">
          answers honestly: second-scale moves are driven by order-book microstructure this platform does not receive —
          shown so the limit is explicit, not hidden.
        </Point>
        <Point k="Track record">
          every directional call is recorded with its price and deadline, scored automatically once the horizon
          matures, and displayed as a measured hit rate. A coin flip scores 50%; judge only samples of 20+.
        </Point>
      </Section>

      <Section title="Autopilot" tag="automation">
        <Point k="What it does">
          trades your paper account automatically from the signals board, on a ~10 minute cycle. Buys top-ranked names
          with a risk-sized plan; sells holdings whose trend broke — unless a protective exit is already resting.
        </Point>
        <Point k="Exit styles">
          <em>Ride the trend</em> (default): trailing stop only, no fixed target — momentum&apos;s profits live in the few
          big runners this style refuses to cut short. <em>Bank at target</em>: classic stop + ~2R target bracket —
          steadier, but caps every win.
        </Point>
        <Point k="Guards">
          max concurrent positions, max capital per trade (positions size down to fit rather than being skipped), one
          trade per symbol per day, cash tracked across the pass, and an automatic pause whenever prices fall back to
          the simulator.
        </Point>
        <Point k="Audit log">
          every buy, sell, skip and error is written down with its reasons. The log is the product — a black box would
          teach you nothing.
        </Point>
        <Point k="Results panel">
          win rate, expectancy per trade, profit factor and max drawdown from your actual closed trades. Judge the
          process on 20–30 closed trades minimum, not the first few.
        </Point>
      </Section>

      <Section title="Watchlist & notifications" tag="workflow">
        <Point k="Auto watchlist">
          your open positions pin themselves to the watchlist automatically (marked <em>held</em>), and today&apos;s top
          momentum picks appear beneath your manual list (marked <em>pick</em>). Manual entries stay yours to add and
          remove.
        </Point>
        <Point k="Notifications">
          the bell in the header enables browser notifications for every fill (manual, autopilot, and bracket exits
          fired in the background) plus autopilot errors. Honest limit: they work while the browser is open — a closed
          browser hears nothing. A Telegram bridge can lift that; it needs a bot token.
        </Point>
      </Section>

      <Section title="What this platform will not pretend" tag="honesty">
        <p className="text-slate-400">
          The momentum edge is small and cyclical — the backtest panel shows the current measurement, not a promise.
          Second-scale prediction is noise without depth data, and the Forecast tab says so. Paper P&amp;L overstates
          live results because fills are free. Everything the system decides is written down with reasons, so you can
          check its work — and its track record is measured against reality, even when the numbers are unflattering.
        </p>
      </Section>
    </div>
  )
}
