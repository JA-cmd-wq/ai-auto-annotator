#!/usr/bin/env bash
# 一键安装（矩池云等租赁 GPU 容器适用，不需要 Docker）。
# 幂等：已装好的组件自动跳过，中断后重跑即可。
# 用法：bash setup.sh [模型目录，默认 /mnt/LocateAnything-3B]
set -euo pipefail
cd "$(dirname "$0")"

MODEL_DIR="${1:-/mnt/LocateAnything-3B}"
GO_VERSION=1.22.12

step() { echo -e "\n\033[1;34m==> $*\033[0m"; }
ok()   { echo -e "\033[1;32m    $*\033[0m"; }
die()  { echo -e "\033[1;31m[错误] $*\033[0m" >&2; exit 1; }

step "0/5 环境检查"
nvidia-smi --query-gpu=name,memory.total --format=csv,noheader 2>/dev/null \
  || die "看不到 NVIDIA GPU（nvidia-smi 失败），本系统需要 GPU"
command -v python3 >/dev/null || die "缺少 python3"
command -v node >/dev/null || die "缺少 node（矩池云镜像一般自带；否则请先装 Node 18+）"
if [ ! -f "$MODEL_DIR/config.json" ]; then
  die "模型目录不存在或不完整: $MODEL_DIR
  请先下载: HF_ENDPOINT=https://hf-mirror.com huggingface-cli download nvidia/LocateAnything-3B --local-dir $MODEL_DIR"
fi
ok "GPU / python3 / node / 模型目录 就绪"

step "1/5 Go 工具链（goproxy.cn 镜像）"
if [ -x /opt/go/bin/go ]; then
  ok "已存在 /opt/go，跳过"
else
  curl -fsSL "https://mirrors.aliyun.com/golang/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz
  tar -xzf /tmp/go.tgz -C /opt && rm /tmp/go.tgz
  ok "Go ${GO_VERSION} 装好"
fi
export PATH=/opt/go/bin:$PATH
go env -w GOPROXY=https://goproxy.cn,direct >/dev/null

step "2/5 Python 依赖（清华 PyPI 镜像）"
# 逐包检查，不能只看 torch——矩池云镜像常有 torch 但没有 torchvision
REQS=backend/engine_python/requirements.txt
MISSING=()
for pkg in torch torchvision transformers tokenizers accelerate safetensors huggingface_hub sentencepiece tiktoken einops peft numpy pillow opencv-python-headless packaging requests; do
  import_name="$pkg"
  case "$pkg" in
    opencv-python-headless) import_name="cv2" ;;
    pillow) import_name="PIL" ;;
    huggingface_hub) import_name="huggingface_hub" ;;
  esac
  if ! python3 -c "import ${import_name}" 2>/dev/null; then
    MISSING+=("$pkg")
  fi
done
if [ ${#MISSING[@]} -eq 0 ]; then
  ok "Python 依赖齐全，跳过"
else
  echo "    缺少: ${MISSING[*]}"
  # 已有 torch 时跳过 torch 本体（避免重下 2GB），其余照常装
  if python3 -c "import torch" 2>/dev/null; then
    grep -vE '^torch==' "$REQS" > /tmp/reqs.txt
  else
    cp "$REQS" /tmp/reqs.txt
  fi
  python3 -m pip install --no-cache-dir -r /tmp/reqs.txt -i https://pypi.tuna.tsinghua.edu.cn/simple
  ok "Python 依赖装好"
fi

step "3/5 编译 Go 后端"
(cd backend && go build -o ./bin/annotator-server ./cmd/server)
ok "backend/bin/annotator-server"

step "4/5 前端依赖（npmmirror 镜像）"
if [ -d frontend/node_modules/vite ]; then
  ok "node_modules 已存在，跳过"
else
  (cd frontend && npm ci --registry=https://registry.npmmirror.com)
  ok "前端依赖装好"
fi

step "5/5 生成后端配置"
if [ -f backend/.env ]; then
  ok "backend/.env 已存在，保留"
else
  cat > backend/.env <<EOF
# OpenAI 兼容 LLM（生成任务配置，必填才可用）
MATPOOL_KEY=
MATPOOL_ENDPOINT=https://token.matpool.com/v1/chat/completions
MATPOOL_LLM=DeepSeek-V4-Pro

LA_ENGINE=auto
LA_PYTHON_URL=http://127.0.0.1:8001
LA_MODEL_PATH=$MODEL_DIR
APP_ADDR=:8088
APP_FRONTEND_ORIGIN=http://localhost:5173
EOF
  ok "已生成 backend/.env（请填入 MATPOOL_KEY）"
fi

echo
echo "=============================================="
echo " 安装完成。启动： bash start.sh"
echo " 停止：           bash stop.sh"
echo " 网页：           http://localhost:5173"
echo "=============================================="
