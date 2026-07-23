"""FastAPI application entrypoint."""
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from . import models  # noqa: F401  (ensures models register on Base.metadata)
from .config import settings
from .database import Base, SessionLocal, engine
from .routers import health, market, orders, portfolio
from .services.groww_client import groww_client
from .services.paper_engine import get_or_create_default_account


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Create tables (starter setup uses create_all; swap for Alembic in prod).
    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
    async with SessionLocal() as session:
        await get_or_create_default_account(session)
    print(f"[startup] market data mode: {groww_client.mode}")
    yield


app = FastAPI(
    title="Groww Paper Trading API",
    version="0.1.0",
    description="Simulated (paper) trading on top of Groww market data.",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.cors_origins_list,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(health.router)
app.include_router(market.router)
app.include_router(orders.router)
app.include_router(portfolio.router)


@app.get("/")
async def root():
    return {
        "name": "Groww Paper Trading API",
        "docs": "/docs",
        "market_data_mode": groww_client.mode,
    }
