FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /usr/local/bin/ocr-dispatcher ./ocr-dispatcher

FROM alpine:3.22
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /usr/local/bin/ocr-dispatcher /usr/local/bin/ocr-dispatcher
ENTRYPOINT ["/usr/local/bin/ocr-dispatcher"]
