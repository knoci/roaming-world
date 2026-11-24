FROM golang:1.24 AS builder

WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct

COPY pkg/go.mod pkg/go.sum ./pkg/
COPY food/go.mod food/go.sum ./food/
RUN cd /src/food && go mod download

COPY pkg ./pkg
COPY food ./food
RUN cd /src/food && make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/food/bin/food /app/food
COPY --from=builder /src/food/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/food", "-conf", "/app/configs"]
