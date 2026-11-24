FROM golang:1.23.3-bookworm AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    GOWORK=off

WORKDIR /src
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY article/go.mod article/go.sum ./article/
RUN cd /src/article && go mod download

COPY pkg ./pkg
COPY article ./article
RUN cd /src/article && make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y \
        && useradd -r -u 10001 -g nogroup -s /sbin/nologin appuser

WORKDIR /app
COPY --from=builder /src/article/bin/article /app/article
COPY --from=builder /src/article/configs /app/configs
RUN chown -R 10001:65534 /app

USER 10001

EXPOSE 8000
EXPOSE 9000

CMD ["/app/article", "-conf", "/app/configs"]
