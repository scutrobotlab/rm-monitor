ARG APP
FROM golang:1.23-alpine as builder
ARG APP

WORKDIR /go/src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -ldflags "-s -w" -trimpath -o /go/bin/app ${APP}/main.go

FROM alpine:3.12

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN apk add --no-cache tzdata ca-certificates curl

COPY --from=builder /go/bin/app /usr/bin/app

ENTRYPOINT ["/usr/bin/app"]