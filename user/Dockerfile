FROM golang:1.24 AS builder

WORKDIR /src
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY user/go.mod user/go.sum ./user/
WORKDIR /src/user
RUN GOPROXY=https://goproxy.cn go mod download

WORKDIR /src
COPY pkg ./pkg
COPY user ./user
WORKDIR /src/user
RUN GOPROXY=https://goproxy.cn make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/user/bin/user /app/user
COPY --from=builder /src/user/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/user", "-conf", "/app/configs"]
