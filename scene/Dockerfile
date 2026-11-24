FROM golang:1.24 AS builder

WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct

COPY pkg/go.mod pkg/go.sum ./pkg/
COPY scene/go.mod scene/go.sum ./scene/
RUN cd /src/scene && go mod download

COPY pkg ./pkg
COPY scene ./scene
RUN cd /src/scene && make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/scene/bin/scene /app/scene
COPY --from=builder /src/scene/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/scene", "-conf", "/app/configs"]
