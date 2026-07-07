#!/usr/bin/env bash
# 一键启动三个服务（sidecar / 后端 / 前端），日志写入 logs/。
# 重复执行安全：已在运行的服务自动跳过。
set -euo pipefail
cd "$(dirname "$0")"
export PATH=/opt/go/bin:$PATH
mkdir -p logs

up() { # up <名字> <健康检查URL> <启动命令...>
  local name=$1 url=$2; shift 2
  if curl -sf --connect-timeout 2 "$url" >/dev/null 2>&1; then
    echo "[$name] 已在运行，跳过"
    return
  fi
  echo "[$name] 启动中..."
  nohup "$@" > "logs/$name.log" 2>&1 &
  echo "[$name] pid=$! 日志=logs/$name.log"
}

up sidecar  http://127.0.0.1:8001/health  bash backend/engine_python/start_sidecar.sh
up backend  http://127.0.0.1:8088/api/v1/health  bash backend/run_backend.sh
up frontend http://127.0.0.1:5173  bash -c 'cd frontend && exec node node_modules/vite/bin/vite.js --host 0.0.0.0 --port 5173'

echo
echo "等待模型加载（首次约 1~2 分钟）..."
for i in $(seq 1 60); do
  if curl -sf http://127.0.0.1:8001/status 2>/dev/null | grep -q '"loaded": true'; then
    echo "模型已加载。打开 http://localhost:5173 开始使用"
    exit 0
  fi
  sleep 5
done
echo "[提示] 模型仍在加载或启动失败，请查看 logs/sidecar.log"
