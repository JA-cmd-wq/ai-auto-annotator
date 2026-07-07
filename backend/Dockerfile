# Go 后端：多阶段构建，运行镜像只有 ~30MB。
# sqlite 用的是 modernc.org/sqlite（纯 Go），无需 cgo。
FROM golang:1.22 AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/annotator-server ./cmd/server

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=build /out/annotator-server /app/annotator-server
# 数据（数据库/上传素材/导出 zip）统一放 /app/data，由 compose 挂载共享卷。
# 注意：backend 与 sidecar 必须以相同路径看到这份数据（服务间传的是绝对路径）。
ENV APP_ADDR=:8080 \
    LA_ENGINE=python \
    LA_PYTHON_URL=http://sidecar:8001
EXPOSE 8080
CMD ["/app/annotator-server"]
