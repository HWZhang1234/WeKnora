#!/bin/bash
set -e
echo "=== 加载 Docker 镜像 ==="
docker load < weknora-images.tar.gz
echo "=== 启动所有服务 ==="
docker compose up -d
echo "=== 完成！==="
echo "前端: http://localhost:80"
echo "后端: http://localhost:8080"
echo "健康检查: curl http://localhost:8080/health"
