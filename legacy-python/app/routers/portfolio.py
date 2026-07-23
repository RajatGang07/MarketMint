from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from ..database import get_session
from ..schemas import PortfolioOut
from ..services.paper_engine import (
    get_or_create_default_account,
    get_portfolio,
    match_open_orders,
    reset_account,
)

router = APIRouter(prefix="/portfolio", tags=["portfolio"])


@router.get("", response_model=PortfolioOut)
async def portfolio(session: AsyncSession = Depends(get_session)):
    account = await get_or_create_default_account(session)
    await match_open_orders(session, account)
    return await get_portfolio(session, account)


@router.post("/reset", response_model=PortfolioOut)
async def reset(session: AsyncSession = Depends(get_session)):
    account = await get_or_create_default_account(session)
    await reset_account(session, account)
    return await get_portfolio(session, account)
