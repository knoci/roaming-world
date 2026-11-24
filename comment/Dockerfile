FROM golang:1.24 AS builder

WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct

COPY pkg/go.mod pkg/go.sum ./pkg/
COPY comment/go.mod comment/go.sum ./comment/
RUN cd /src/comment && go mod download

COPY pkg ./pkg
COPY comment ./comment
RUN cd /src/comment && make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/comment/bin/comment /app/comment
COPY --from=builder /src/comment/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/comment", "-conf", "/app/configs"]
