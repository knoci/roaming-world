FROM golang:1.24 AS builder

WORKDIR /src
COPY . .
RUN GOPROXY=https://goproxy.cn make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/bin/scene /app/scene
COPY --from=builder /src/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/scene", "-conf", "/app/configs"]
