# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS builder

WORKDIR /go/src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags "-s -w" -trimpath -o /usr/local/bin/recording-repair ./cmd/recording-repair

FROM alpine:3.20

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /usr/local/bin/recording-repair /usr/local/bin/recording-repair

ENTRYPOINT ["/usr/local/bin/recording-repair"]
