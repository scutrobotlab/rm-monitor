# syntax=docker/dockerfile:1.7
ARG APP
FROM golang:1.26-alpine AS builder
ARG APP

WORKDIR /go/src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-s -w" -trimpath -o /usr/local/bin/app ${APP}/main.go

FROM alpine:3.20
ARG APP

ENV TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates tzdata \
    && case "$APP" in \
        record-job|stt-job) apk add --no-cache ffmpeg ;; \
        transcode-job) apk add --no-cache ffmpeg ;; \
        *) true ;; \
    esac

WORKDIR /app
COPY --from=builder /usr/local/bin/app /usr/local/bin/app

ENTRYPOINT ["/usr/local/bin/app"]
