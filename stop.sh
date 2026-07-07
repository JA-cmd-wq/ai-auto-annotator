#!/usr/bin/env bash
# 停止 setup.sh/start.sh 启动的三个服务
cd "$(dirname "$0")"
pkill -f 'bin/annotator-server'      && echo '[backend] 已停止'  || echo '[backend] 未在运行'
pkill -f 'engine_python/server.py'   && echo '[sidecar] 已停止'  || echo '[sidecar] 未在运行'
pkill -f 'vite.js .*--port 5173'     && echo '[frontend] 已停止' || echo '[frontend] 未在运行'
