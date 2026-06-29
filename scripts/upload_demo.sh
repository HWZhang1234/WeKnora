#!/bin/bash
# WeKnora 文档上传 API Demo
# 用法: ./upload_demo.sh <文件路径>
# 示例: ./upload_demo.sh /path/to/KBA-260514191553_REV_3_PCIe_xxx.pdf

set -e

# ========== 配置 ==========
API_BASE="http://localhost:8080/api/v1"
API_KEY="your_api_key"                    # 替换为你的 API Key（在前端账户信息页面获取）
KB_ID="8fd676ff-66f7-48f2-a74b-a727b7f5fc01"  # 替换为你的知识库ID

# ========== 参数检查 ==========
if [ -z "$1" ]; then
    echo "用法: $0 <文件路径>"
    echo "示例: $0 ./KBA-260514191553_REV_3_PCIe.pdf"
    exit 1
fi

FILE_PATH="$1"
if [ ! -f "$FILE_PATH" ]; then
    echo "错误: 文件不存在: $FILE_PATH"
    exit 1
fi

echo "========================================="
echo "上传文件: $FILE_PATH"
echo "知识库ID: $KB_ID"
echo "========================================="

# ========== 上传 ==========
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
    "${API_BASE}/knowledge-bases/${KB_ID}/knowledge/file" \
    -H "X-API-Key: ${API_KEY}" \
    -F "file=@${FILE_PATH}")

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

echo ""
echo "HTTP 状态码: $HTTP_CODE"
echo "响应内容:"
echo "$BODY" | python3 -m json.tool 2>/dev/null || echo "$BODY"

# ========== 结果判断 ==========
echo ""
if [ "$HTTP_CODE" = "200" ]; then
    echo "✅ 上传成功！文档正在解析中..."
elif [ "$HTTP_CODE" = "409" ]; then
    echo "⚠️  上传冲突（重复文件或版本冲突）"
else
    echo "❌ 上传失败"
fi
