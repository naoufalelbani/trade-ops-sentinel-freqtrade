FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags='-s -w' -o /out/bnb-fees-monitor ./cmd/bnb-fees-monitor

FROM alpine:3.20
RUN adduser -D -g '' app
USER app
WORKDIR /app
COPY --from=builder /out/bnb-fees-monitor /app/bnb-fees-monitor
ENTRYPOINT ["/app/bnb-fees-monitor"]
