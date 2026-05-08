# syntax=docker/dockerfile:1.7
ARG APP
FROM golang:1.23-bookworm AS builder
ARG APP

WORKDIR /go/src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-s -w" -trimpath -o /go/bin/app ${APP}/main.go

FROM debian:bookworm-slim

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && ffmpeg -hide_banner -encoders 2>/dev/null | grep -q libsvtav1

COPY --from=builder /go/bin/app /usr/bin/app

ENTRYPOINT ["/usr/bin/app"]
