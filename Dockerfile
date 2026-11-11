# 多阶段构建：编译期用 Go 工具链，运行期只剩静态二进制 + 静态资源。
# 国内服务器若无法访问 Docker Hub，可配置镜像加速，或通过 compose 的
# build.args / .env 中的 GO_IMAGE、RUNTIME_IMAGE 换成可拉取的镜像源。
ARG GO_IMAGE=golang:1.22-alpine
ARG RUNTIME_IMAGE=alpine:3.20

FROM ${GO_IMAGE} AS build
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOOS=linux
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/order-server .

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/order-server ./order-server
COPY migrations ./migrations
COPY web ./web
RUN chown -R app:app /app
USER app
ENV TZ=Asia/Shanghai \
    APP_ENV=production \
    APP_ADDR=:8090 \
    APP_STORE=mysql \
    APP_WEB=./web \
    APP_MIGRATIONS=./migrations
EXPOSE 8090
# /api/meta 无需登录，适合做容器健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8090/api/meta || exit 1
ENTRYPOINT ["./order-server"]
