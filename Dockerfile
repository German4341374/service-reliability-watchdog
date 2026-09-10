FROM golang:1.26.7-alpine3.23@sha256:b17af760035fc2f338eed92d448a6c67f2d45438844fc6c60678fa5f99e44b57 AS builder

ARG VERSION=0.1.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/watchdog ./cmd/watchdog \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-target ./cmd/demo-target \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/certgen ./cmd/certgen \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

FROM alpine:3.23.3 AS runtime

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
