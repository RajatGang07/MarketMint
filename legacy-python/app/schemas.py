"""Pydantic request/response schemas."""
from datetime import datetime
from typing import List, Optional

from pydantic import BaseModel, ConfigDict, Field


class OrderRequest(BaseModel):
    trading_symbol: str = Field(..., examples=["RELIANCE"])
    exchange: str = "NSE"
    segment: str = "CASH"
    product: str = "CNC"
    transaction_type: str = Field(..., description="BUY or SELL")
    order_type: str = Field("MARKET", description="MARKET or LIMIT")
    quantity: int = Field(..., gt=0)
    limit_price: Optional[float] = Field(None, gt=0, description="Required for LIMIT orders")


class OrderOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    order_ref: str
    trading_symbol: str
    exchange: str
    segment: str
    product: str
    transaction_type: str
    order_type: str
    quantity: int
    limit_price: Optional[float]
    status: str
    fill_price: Optional[float]
    filled_quantity: int
    message: Optional[str]
    created_at: datetime


class PositionOut(BaseModel):
    trading_symbol: str
    exchange: str
    segment: str
    quantity: int
    avg_price: float
    realized_pnl: float
    ltp: float = 0.0
    market_value: float = 0.0
    unrealized_pnl: float = 0.0


class PortfolioOut(BaseModel):
    account_name: str
    starting_cash: float
    cash: float
    invested: float
    market_value: float
    equity: float
    realized_pnl: float
    unrealized_pnl: float
    total_pnl: float
    total_pnl_pct: float
    positions: List[PositionOut]


class Ltp(BaseModel):
    symbol: str
    exchange: str
    ltp: float


class QuoteOut(BaseModel):
    symbol: str
    exchange: str
    last_price: float
    open: float
    high: float
    low: float
    close: float
    volume: float = 0.0
