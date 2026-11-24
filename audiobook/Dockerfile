FROM golang:1.24 AS builder

WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct

COPY pkg/go.mod pkg/go.sum ./pkg/
COPY audiobook/go.mod audiobook/go.sum ./audiobook/
RUN cd /src/audiobook && go mod download

COPY pkg ./pkg
COPY audiobook ./audiobook
RUN cd /src/audiobook && make build VERSION=dev

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        netbase \
        tzdata \
        && rm -rf /var/lib/apt/lists/* \
        && apt-get autoremove -y && apt-get autoclean -y

WORKDIR /app
COPY --from=builder /src/audiobook/bin/audiobook /app/audiobook
COPY --from=builder /src/audiobook/configs /app/configs

EXPOSE 8000
EXPOSE 9000
VOLUME /data/conf

CMD ["/app/audiobook", "-conf", "/app/configs"]
