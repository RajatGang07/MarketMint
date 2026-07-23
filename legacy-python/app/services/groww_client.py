"""Groww market-data client with a deterministic mock fallback.

When ``USE_MOCK_MARKET_DATA`` is true (or no access token / the SDK fails to
initialise), prices are simulated so the platform runs fully offline and
outside market hours. Set it to false with a valid access token for live data.
"""
import hashlib
import math
import random
import time
from typing import Dict

from ..config import settings

# Map our short codes to the attribute names Groww's SDK exposes as constants.
_EXCHANGE_ATTR = {"NSE": "EXCHANGE_NSE", "BSE": "EXCHANGE_BSE"}
_SEGMENT_ATTR = {
    "CASH": "SEGMENT_CASH",
    "FNO": "SEGMENT_FNO",
    "COMMODITY": "SEGMENT_COMMODITY",
}


class MockMarketData:
    """Simulated prices so the app works without live Groww access."""

    _BASE: Dict[str, float] = {
        "RELIANCE": 2900.0, "TCS": 3850.0, "INFY": 1650.0, "HDFCBANK": 1700.0,
        "SBIN": 820.0, "ICICIBANK": 1250.0, "WIPRO": 480.0, "ITC": 460.0,
        "HINDUNILVR": 2450.0, "BHARTIARTL": 1600.0,
        "NIFTY": 24500.0, "BANKNIFTY": 52000.0,
    }

    def _base_price(self, symbol: str) -> float:
        if symbol in self._BASE:
            return self._BASE[symbol]
        # Stable synthetic base price derived from the symbol name.
        h = int(hashlib.sha256(symbol.encode()).hexdigest(), 16)
        return 100.0 + float(h % 4000)

    def ltp(self, symbol: str) -> float:
        base = self._base_price(symbol)
        # Slow sine drift (+/-1%) so prices move over time, plus tiny noise.
        drift = math.sin(time.time() / 30.0 + (hash(symbol) % 7)) * 0.01
        noise = (random.random() - 0.5) * 0.002
        return round(base * (1 + drift + noise), 2)

    def ohlc(self, symbol: str) -> Dict[str, float]:
        base = self._base_price(symbol)
        last = self.ltp(symbol)
        return {
            "open": round(base, 2),
            "high": round(max(base, last) * 1.005, 2),
            "low": round(min(base, last) * 0.995, 2),
            "close": round(base, 2),
        }


class GrowwClient:
    """Thin wrapper over the growwapi SDK with a mock fallback.

    All methods are synchronous (the SDK is blocking); callers should run them
    in a threadpool from async code.
    """

    def __init__(self) -> None:
        self._mock = MockMarketData()
        self._sdk = None
        self.mode = "mock"

        if not settings.use_mock_market_data and settings.groww_access_token:
            try:
                from growwapi import GrowwAPI  # imported lazily

                self._sdk = GrowwAPI(settings.groww_access_token)
                self.mode = "live"
            except Exception as exc:  # noqa: BLE001 - fall back to mock, keep running
                print(f"[groww] SDK init failed ({exc!r}); using mock market data.")
                self._sdk = None
                self.mode = "mock"

    # -- constant lookups -------------------------------------------------
    def _exchange_const(self, exchange: str):
        return getattr(self._sdk, _EXCHANGE_ATTR.get(exchange.upper(), "EXCHANGE_NSE"))

    def _segment_const(self, segment: str):
        return getattr(self._sdk, _SEGMENT_ATTR.get(segment.upper(), "SEGMENT_CASH"))

    # -- market data ------------------------------------------------------
    def get_ltp(self, exchange: str, segment: str, trading_symbol: str) -> float:
        if self._sdk is None:
            return self._mock.ltp(trading_symbol)
        key = f"{exchange.upper()}_{trading_symbol.upper()}"
        resp = self._sdk.get_ltp(
            segment=self._segment_const(segment),
            exchange_trading_symbols=(key,),
        )
        if isinstance(resp, dict) and resp:
            return float(next(iter(resp.values())))
        return 0.0

    def get_quote(self, exchange: str, segment: str, trading_symbol: str) -> Dict[str, float]:
        if self._sdk is None:
            ohlc = self._mock.ohlc(trading_symbol)
            return {"last_price": self._mock.ltp(trading_symbol), "volume": 0.0, **ohlc}
        quote = self._sdk.get_quote(
            exchange=self._exchange_const(exchange),
            segment=self._segment_const(segment),
            trading_symbol=trading_symbol.upper(),
        )
        quote = quote if isinstance(quote, dict) else {}
        ohlc = quote.get("ohlc") or {}
        return {
            "last_price": float(quote.get("last_price", 0.0) or 0.0),
            "open": float(ohlc.get("open", 0.0) or 0.0),
            "high": float(ohlc.get("high", 0.0) or 0.0),
            "low": float(ohlc.get("low", 0.0) or 0.0),
            "close": float(ohlc.get("close", 0.0) or 0.0),
            "volume": float(quote.get("volume", 0.0) or 0.0),
        }


# Module-level singleton used across the app.
groww_client = GrowwClient()
