FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags='-s -w' -o /out/trade-ops-sentinel ./cmd/trade-ops-sentinel

FROM alpine:3.20
RUN adduser -D -g '' app
USER app
WORKDIR /app
COPY --from=builder /out/trade-ops-sentinel /app/trade-ops-sentinel
ENTRYPOINT ["/app/trade-ops-sentinel"]
