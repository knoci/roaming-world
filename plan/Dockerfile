FROM golang:1.24 AS builder

WORKDIR /src
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY plan/go.mod plan/go.sum ./plan/
RUN cd /src/plan && GOPROXY=https://goproxy.cn go mod download

COPY pkg ./pkg
COPY plan ./plan
WORKDIR /src/plan
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
COPY --from=builder /src/plan/bin/plan /app/plan
COPY --from=builder /src/plan/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/plan", "-conf", "/app/configs"]
