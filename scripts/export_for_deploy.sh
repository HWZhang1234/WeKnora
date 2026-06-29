#!/bin/bash
# WeKnora 一键导出脚本 — 打包所有镜像 + 配置，拿到另一台服务器直接跑
set -e

EXPORT_DIR="./weknora-export"
rm -rf "$EXPORT_DIR"
mkdir -p "$EXPORT_DIR"

echo "=== 1/3 导出所有 Docker 镜像 ==="
docker save \
  wechatopenai/weknora-app:latest \
  wechatopenai/weknora-docreader:latest \
  wechatopenai/weknora-ui:latest \
  neo4j:2025.10.1 \
  paradedb/paradedb:v0.22.2-pg17 \
  redis:7.0-alpine \
  | gzip > "$EXPORT_DIR/weknora-images.tar.gz"

echo "=== 2/3 复制配置文件 ==="
cp docker-compose.yml "$EXPORT_DIR/"
cp .env "$EXPORT_DIR/"
cp -r config/ "$EXPORT_DIR/config/"

echo "=== 3/3 生成启动脚本 ==="
cat > "$EXPORT_DIR/start.sh" << 'EOF'
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
EOF
chmod +x "$EXPORT_DIR/start.sh"

echo ""
echo "========================================="
echo "导出完成！文件在: $EXPORT_DIR/"
ls -lh "$EXPORT_DIR/"
echo ""
echo "使用方法："
echo "  1. 把 $EXPORT_DIR/ 整个文件夹拷贝到目标服务器"
echo "  2. 修改 .env 中的 IP 地址（模型服务、Milvus 等）"
echo "  3. 执行 ./start.sh"
echo "========================================="
