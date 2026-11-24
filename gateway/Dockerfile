FROM golang:1.24 AS builder

WORKDIR /src
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY gateway/go.mod gateway/go.sum ./gateway/
RUN cd /src/gateway && GOPROXY=https://goproxy.cn go mod download

COPY pkg ./pkg
COPY gateway ./gateway
RUN cd /src/gateway && GOPROXY=https://goproxy.cn make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/gateway/bin/gateway /app/gateway
COPY --from=builder /src/gateway/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/gateway", "-conf", "/app/configs"]
