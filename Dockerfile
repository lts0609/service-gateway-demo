# STEP1
FROM m.daocloud.io/docker.io/golang:1.24-alpine as builder

WORKDIR /app

ENV GOPROXY="https://goproxy.cn,direct"

ENV CGO_ENABLED=0

ENV GO111MODULE=on

COPY . ./

RUN go mod tidy

RUN go build -trimpath -ldflags "-s -w" -o service-gateway ./main.go

# STEP2
FROM m.daocloud.io/docker.io/alpine:latest

WORKDIR /app

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache openssh tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && apk del tzdata \
    && rm -rf /var/cache/apk/*

COPY --from=builder --chmod=755 /app/service-gateway .

EXPOSE 8080

CMD ["./service-gateway"]