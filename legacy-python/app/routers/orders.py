from typing import List

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..database import get_session
from ..models import Order
from ..schemas import OrderOut, OrderRequest
from ..services.paper_engine import (
    OrderError,
    get_or_create_default_account,
    match_open_orders,
    place_order,
)

router = APIRouter(prefix="/orders", tags=["orders"])


@router.post("", response_model=OrderOut)
async def create_order(req: OrderRequest, session: AsyncSession = Depends(get_session)):
    account = await get_or_create_default_account(session)
    try:
        return await place_order(session, account, req)
    except OrderError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@router.get("", response_model=List[OrderOut])
async def list_orders(session: AsyncSession = Depends(get_session)):
    account = await get_or_create_default_account(session)
    # Opportunistically fill any OPEN limit orders that are now marketable.
    await match_open_orders(session, account)
    result = await session.execute(
        select(Order)
        .where(Order.account_id == account.id)
        .order_by(Order.created_at.desc())
        .limit(100)
    )
    return list(result.scalars().all())
