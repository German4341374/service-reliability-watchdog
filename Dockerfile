FROM golang:1.26.5-alpine3.23 AS builder

ARG VERSION=0.1.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/watchdog ./cmd/watchdog \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-target ./cmd/demo-target \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/certgen ./cmd/certgen \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

FROM alpine:3.24.1 AS runtime

RUN addgroup -S -g 10001 watchdog \
    && adduser -S -D -H -u 10001 -G watchdog watchdog
COPY --from=builder /out/watchdog /watchdog
COPY --from=builder /out/demo-target /demo-target
COPY --from=builder /out/certgen /certgen
COPY --from=builder /out/healthcheck /healthcheck

USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD ["/healthcheck", "http://127.0.0.1:8080/health/ready"]
ENTRYPOINT ["/watchdog"]
CMD ["-config", "/etc/watchdog/config.yaml"]

