# Python 推理 sidecar：常驻 LocateAnything-3B（需要 NVIDIA GPU）。
# 基础镜像自带 torch 2.6 + CUDA 12.4，避免学生再下 2GB 的 torch wheel。
# 模型权重（~7.3GB）不打进镜像，由 compose 挂载到 /models/LocateAnything-3B。
FROM pytorch/pytorch:2.6.0-cuda12.4-cudnn9-runtime

WORKDIR /app
COPY requirements.txt .
# 基础镜像已含 torch 2.6.0，跳过它以免重复下载 2GB+ 的 wheel；
# torchvision 体积小且版本要求严格，保留由 pip 校验安装
RUN grep -vE '^torch==' requirements.txt > /tmp/reqs.txt && \
    pip install --no-cache-dir -r /tmp/reqs.txt \
      -i https://pypi.tuna.tsinghua.edu.cn/simple

COPY server.py .

ENV LA_MODEL_PATH=/models/LocateAnything-3B \
    LA_HOST=0.0.0.0 \
    LA_PORT=8001
EXPOSE 8001
CMD ["python3", "server.py"]
