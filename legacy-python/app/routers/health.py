from fastapi import APIRouter

from ..services.groww_client import groww_client

router = APIRouter(tags=["health"])


@router.get("/health")
async def health():
    return {"status": "ok", "market_data_mode": groww_client.mode}
