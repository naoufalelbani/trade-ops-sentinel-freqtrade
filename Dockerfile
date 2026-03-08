FROM golang:1.22-alpine AS builder
WORKDIR /src
ARG APP_VERSION=dev
ARG APP_COMMIT=none
ARG APP_BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64
COPY go.mod ./
COPY go.sum ./
COPY CHANGELOG.md ./
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.appVersion=${APP_VERSION} -X main.appCommit=${APP_COMMIT} -X main.appBuildDate=${APP_BUILD_DATE}" -o /out/trade-ops-sentinel ./cmd/trade-ops-sentinel

FROM alpine:3.20
RUN adduser -D -g '' app
USER app
WORKDIR /app
COPY --from=builder /out/trade-ops-sentinel /app/trade-ops-sentinel
COPY --from=builder /src/CHANGELOG.md /app/CHANGELOG.md
ENTRYPOINT ["/app/trade-ops-sentinel"]
