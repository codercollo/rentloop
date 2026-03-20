# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Cache dependencies before copying source
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w" -o /rentloop ./cmd/server

# ── Run stage ─────────────────────────────────────────────────────────────────
FROM alpine:3.19

# ca-certificates for HTTPS calls (Twilio, AT, Daraja)
# tzdata for Africa/Nairobi timezone in digest cron
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /rentloop .

# Migrations are run separately by cmd/migrate before the server starts
COPY internal/db/migrations ./internal/db/migrations

EXPOSE 8080

ENTRYPOINT ["./rentloop"]