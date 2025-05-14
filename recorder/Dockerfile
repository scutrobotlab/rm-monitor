ARG APP
FROM golang:1.23-alpine as builder
ARG APP

WORKDIR /go/src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -ldflags "-s -w" -trimpath -o /go/bin/app ${APP}/main.go

FROM python:3.10-alpine3.14

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN pip install -i https://pypi.tuna.tsinghua.edu.cn/simple streamlink  \
    && apk add --no-cache ca-certificates curl ffmpeg tzdata

COPY --from=builder /go/bin/app /usr/bin/app

ENTRYPOINT ["/usr/bin/app"]