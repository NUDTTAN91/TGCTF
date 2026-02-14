# 构建阶段
FROM golang:1.23-alpine AS builder

WORKDIR /app

# 设置代理加速下载
ENV GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0

# 先复制依赖文件，利用缓存
COPY go.mod ./

# 复制源代码并编译
COPY . .
RUN go mod tidy && go build -ldflags="-s -w" -o tgctf ./server

# 运行阶段
FROM alpine:latest

LABEL Author="tan91"
LABEL GitHub="https://github.com/NUDTTAN91"
LABEL Blog="https://blog.csdn.net/ZXW_NUDT"

WORKDIR /app

# 配置清华源加速
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories

# 设置时区为中国，安装Docker CLI和AWD-F所需工具
RUN apk add --no-cache tzdata docker-cli curl unzip && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

# 复制编译好的二进制
COPY --from=builder /app/tgctf .
# 复制前端静态文件
COPY --from=builder /app/web ./web

EXPOSE 80

CMD ["./tgctf"]
