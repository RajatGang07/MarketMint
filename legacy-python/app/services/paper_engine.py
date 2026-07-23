"""Paper trading engine.

Simulates order execution against live/mock market prices and keeps the
virtual account's cash, positions and realised/unrealised P&L in sync.

Scope (v1): long-only cash equities. MARKET orders fill immediately at the
last traded price; LIMIT orders fill when marketable, otherwise they stay OPEN
and are matched opportunistically. Shorting and F&O are intentionally out of
scope and flagged where relevant.
"""
import uuid
from typing import List, Optional

from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession
from starlette.concurrency import run_in_threadpool

from ..config import settings
from ..models import Account, Order, Position, Trade
from ..schemas import OrderRequest, PortfolioOut, PositionOut
from .groww_client import groww_client


class OrderError(Exception):
    """Raised when an order cannot be accepted or filled."""


async def get_or_create_default_account(session: AsyncSession) -> Account:
    result = await session.execute(
        select(Account).where(Account.name == settings.default_account_name)
    )
    account = result.scalar_one_or_none()
    if account is None:
        account = Account(
            name=settings.default_account_name,
            starting_cash=settings.starting_cash,
            cash=settings.starting_cash,
        )
        session.add(account)
        await session.commit()
        await session.refresh(account)
    return account


async def _ltp(exchange: str, segment: str, symbol: str) -> float:
    return await run_in_threadpool(groww_client.get_ltp, exchange, segment, symbol)


async def _get_position(
    session: AsyncSession, account_id: int, symbol: str, segment: str
) -> Optional[Position]:
    result = await session.execute(
        select(Position).where(
            Position.account_id == account_id,
            Position.trading_symbol == symbol,
            Position.segment == segment,
        )
    )
    return result.scalar_one_or_none()


def _compute_fill(
    order_type: str, transaction_type: str, ltp: float, limit_price: Optional[float]
) -> Optional[float]:
    """Return the fill price, or None if a LIMIT order is not marketable yet."""
    if order_type == "MARKET":
        return ltp
    if limit_price is None:
        return None
    if transaction_type == "BUY":
        return min(ltp, limit_price) if ltp <= limit_price else None
    return max(ltp, limit_price) if ltp >= limit_price else None


async def place_order(
    session: AsyncSession, account: Account, req: OrderRequest
) -> Order:
    symbol = req.trading_symbol.upper().strip()
    txn = req.transaction_type.upper()
    otype = req.order_type.upper()

    if txn not in ("BUY", "SELL"):
        raise OrderError("transaction_type must be BUY or SELL")
    if otype not in ("MARKET", "LIMIT"):
        raise OrderError("order_type must be MARKET or LIMIT")
    if otype == "LIMIT" and not req.limit_price:
        raise OrderError("limit_price is required for LIMIT orders")

    ltp = await _ltp(req.exchange, req.segment, symbol)

    order = Order(
        account_id=account.id,
        order_ref=uuid.uuid4().hex[:20],
        trading_symbol=symbol,
        exchange=req.exchange.upper(),
        segment=req.segment.upper(),
        product=req.product.upper(),
        transaction_type=txn,
        order_type=otype,
        quantity=req.quantity,
        limit_price=req.limit_price,
        status="OPEN",
        filled_quantity=0,
    )

    fill_price = _compute_fill(otype, txn, ltp, req.limit_price)
    if fill_price is None:
        order.message = f"Order OPEN — waiting to fill (LTP {ltp})."
        session.add(order)
        await session.commit()
        await session.refresh(order)
        return order

    try:
        await _apply_fill(session, account, order, fill_price)
        order.status = "FILLED"
        order.fill_price = round(fill_price, 2)
        order.filled_quantity = req.quantity
        order.message = "Filled (paper)."
    except OrderError as exc:
        order.status = "REJECTED"
        order.message = str(exc)

    session.add(order)
    await session.commit()
    await session.refresh(order)
    return order


async def _apply_fill(
    session: AsyncSession, account: Account, order: Order, fill_price: float
) -> None:
    """Mutate cash + position for a fill. Raises OrderError if not allowed."""
    qty = order.quantity
    value = fill_price * qty
    position = await _get_position(
        session, account.id, order.trading_symbol, order.segment
    )

    if order.transaction_type == "BUY":
        if account.cash < value:
            raise OrderError(
                f"Insufficient funds: need {value:.2f}, have {account.cash:.2f}"
            )
        account.cash -= value
        if position is None:
            position = Position(
                account_id=account.id,
                trading_symbol=order.trading_symbol,
                exchange=order.exchange,
                segment=order.segment,
                quantity=qty,
                avg_price=fill_price,
                realized_pnl=0.0,
            )
            session.add(position)
        else:
            new_qty = position.quantity + qty
            position.avg_price = (position.avg_price * position.quantity + value) / new_qty
            position.quantity = new_qty
        realized = 0.0
    else:  # SELL — long-only in v1
        if position is None or position.quantity < qty:
            held = 0 if position is None else position.quantity
            raise OrderError(
                f"Cannot sell {qty} {order.trading_symbol}: only {held} held "
                f"(shorting not supported in v1)"
            )
        realized = (fill_price - position.avg_price) * qty
        position.realized_pnl += realized
        account.cash += value
        position.quantity -= qty
        if position.quantity == 0:
            position.avg_price = 0.0

    session.add(
        Trade(
            account_id=account.id,
            order_ref=order.order_ref,
            trading_symbol=order.trading_symbol,
            transaction_type=order.transaction_type,
            quantity=qty,
            price=fill_price,
            realized_pnl=realized,
        )
    )
    session.add(account)


async def match_open_orders(session: AsyncSession, account: Account) -> int:
    """Try to fill any OPEN (limit) orders at the current price. Returns count filled."""
    result = await session.execute(
        select(Order).where(
            Order.account_id == account.id, Order.status == "OPEN"
        )
    )
    filled = 0
    for order in result.scalars().all():
        ltp = await _ltp(order.exchange, order.segment, order.trading_symbol)
        fill_price = _compute_fill(
            order.order_type, order.transaction_type, ltp, order.limit_price
        )
        if fill_price is None:
            continue
        try:
            await _apply_fill(session, account, order, fill_price)
            order.status = "FILLED"
            order.fill_price = round(fill_price, 2)
            order.filled_quantity = order.quantity
            order.message = "Filled (paper)."
            filled += 1
        except OrderError as exc:
            order.status = "REJECTED"
            order.message = str(exc)
    await session.commit()
    return filled


async def get_portfolio(session: AsyncSession, account: Account) -> PortfolioOut:
    all_positions = (
        await session.execute(
            select(Position).where(Position.account_id == account.id)
        )
    ).scalars().all()

    positions_out: List[PositionOut] = []
    invested = market_value = unrealized = realized_total = 0.0

    for pos in all_positions:
        realized_total += pos.realized_pnl
        if pos.quantity == 0:
            continue
        ltp = await _ltp(pos.exchange, pos.segment, pos.trading_symbol)
        mv = ltp * pos.quantity
        cost = pos.avg_price * pos.quantity
        upnl = mv - cost
        invested += cost
        market_value += mv
        unrealized += upnl
        positions_out.append(
            PositionOut(
                trading_symbol=pos.trading_symbol,
                exchange=pos.exchange,
                segment=pos.segment,
                quantity=pos.quantity,
                avg_price=round(pos.avg_price, 2),
                realized_pnl=round(pos.realized_pnl, 2),
                ltp=round(ltp, 2),
                market_value=round(mv, 2),
                unrealized_pnl=round(upnl, 2),
            )
        )

    equity = account.cash + market_value
    total_pnl = equity - account.starting_cash
    total_pnl_pct = (
        (total_pnl / account.starting_cash * 100) if account.starting_cash else 0.0
    )

    return PortfolioOut(
        account_name=account.name,
        starting_cash=round(account.starting_cash, 2),
        cash=round(account.cash, 2),
        invested=round(invested, 2),
        market_value=round(market_value, 2),
        equity=round(equity, 2),
        realized_pnl=round(realized_total, 2),
        unrealized_pnl=round(unrealized, 2),
        total_pnl=round(total_pnl, 2),
        total_pnl_pct=round(total_pnl_pct, 2),
        positions=positions_out,
    )


async def reset_account(session: AsyncSession, account: Account) -> None:
    """Wipe positions/orders/trades and restore starting cash."""
    for model in (Position, Order, Trade):
        await session.execute(delete(model).where(model.account_id == account.id))
    account.cash = account.starting_cash
    session.add(account)
    await session.commit()
