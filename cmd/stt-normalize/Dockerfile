FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags "-s -w" -trimpath -o /usr/local/bin/stt-normalize ./cmd/stt-normalize

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /usr/local/bin/stt-normalize /usr/local/bin/stt-normalize
ENTRYPOINT ["/usr/local/bin/stt-normalize"]
