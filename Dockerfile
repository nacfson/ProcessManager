FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
# BuildKit/buildx sets TARGETOS/TARGETARCH per --platform (amd64, arm64, ...).
ARG TARGETOS=linux
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/manager ./cmd/manager \
    && GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/auth-service ./cmd/auth \
    && GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/sync-admins ./cmd/sync-admins

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -r appuser && useradd -r -g appuser -u 1000 appuser
WORKDIR /app
COPY --from=builder /out/manager ./
COPY --from=builder /out/auth-service ./
COPY --from=builder /out/sync-admins ./
COPY --from=builder /src/web/templates ./web/templates
RUN chown -R appuser:appuser /app
USER appuser
ENV PORT=8080
EXPOSE 8080
# Default is the dashboard. Compose overrides this to ./auth-service for central auth.
ENTRYPOINT ["./manager"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${PORT}/healthz" || exit 1
