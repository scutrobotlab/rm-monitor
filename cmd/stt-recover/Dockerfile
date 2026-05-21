# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS builder

WORKDIR /go/src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-s -w" -trimpath -o /usr/local/bin/stt-recover ./cmd/stt-recover

FROM alpine:3.20

ENV TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app
COPY --from=builder /usr/local/bin/stt-recover /usr/local/bin/stt-recover

ENTRYPOINT ["/usr/local/bin/stt-recover"]
