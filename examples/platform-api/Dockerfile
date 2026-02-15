# ── Build stage ──────────────────────────────────────────────────
FROM golang:1.20-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /platform-api .

# ── Runtime stage ────────────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates
COPY --from=builder /platform-api /usr/local/bin/platform-api

EXPOSE 8080
ENTRYPOINT ["platform-api"]
