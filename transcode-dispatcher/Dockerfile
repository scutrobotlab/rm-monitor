# syntax=docker/dockerfile:1.7
ARG APP
FROM golang:1.23-alpine AS builder
ARG APP

WORKDIR /go/src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-s -w" -trimpath -o /go/bin/app ${APP}/main.go

FROM alpine:3.20

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /go/bin/app /usr/bin/app

ENTRYPOINT ["/usr/bin/app"]
