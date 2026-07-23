# MarketMint — one image, one process: Go API + embedded React dashboard.
# Built for free-tier hosts (Render/Koyeb/Fly): small, fast cold starts.

# --- 1. Frontend ------------------------------------------------------------
FROM node:22-alpine AS ui
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# --- 2. Backend -------------------------------------------------------------
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Swap the placeholder for the real UI, then build a static binary.
RUN rm -rf internal/web/dist
COPY --from=ui /app/dist internal/web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/server ./cmd/server

# --- 3. Runtime -------------------------------------------------------------
FROM alpine:3.20
# TLS roots for Yahoo/Groww/Neon; the binary uses fixed IST offsets, no tzdata needed.
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
USER app
COPY --from=build /out/server /server
ENV PORT=8000
EXPOSE 8000
ENTRYPOINT ["/server"]
