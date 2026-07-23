from typing import List

from fastapi import APIRouter, Query
from starlette.concurrency import run_in_threadpool

from ..schemas import Ltp, QuoteOut
from ..services.groww_client import groww_client

router = APIRouter(prefix="/market", tags=["market"])


@router.get("/ltp", response_model=List[Ltp])
async def get_ltp(
    symbols: str = Query(..., description="Comma-separated symbols, e.g. RELIANCE,TCS"),
    exchange: str = "NSE",
    segment: str = "CASH",
):
    out: List[Ltp] = []
    for sym in [s.strip().upper() for s in symbols.split(",") if s.strip()]:
        price = await run_in_threadpool(groww_client.get_ltp, exchange, segment, sym)
        out.append(Ltp(symbol=sym, exchange=exchange, ltp=round(price, 2)))
    return out


@router.get("/quote", response_model=QuoteOut)
async def get_quote(symbol: str, exchange: str = "NSE", segment: str = "CASH"):
    symbol = symbol.upper()
    quote = await run_in_threadpool(groww_client.get_quote, exchange, segment, symbol)
    return QuoteOut(symbol=symbol, exchange=exchange, **quote)
