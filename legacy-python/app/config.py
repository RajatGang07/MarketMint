"""Application configuration, loaded from environment / .env file."""
from typing import List

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env", env_file_encoding="utf-8", extra="ignore"
    )

    # Database
    database_url: str = "postgresql+asyncpg://paper:paper@localhost:5432/paper_trading"

    # Groww credentials
    groww_access_token: str = ""
    groww_api_secret: str = ""

    # Market data mode: True => simulated prices (no live Groww calls)
    use_mock_market_data: bool = True

    # Paper account defaults
    starting_cash: float = 1_000_000.0
    default_account_name: str = "default"

    # CORS (comma-separated string in env; exposed as a list below)
    cors_origins: str = "http://localhost:5173,http://127.0.0.1:5173"

    @property
    def cors_origins_list(self) -> List[str]:
        return [o.strip() for o in self.cors_origins.split(",") if o.strip()]


settings = Settings()
