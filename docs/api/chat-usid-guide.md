# Chat 接口使用指南（usid 权限版）

## 前提条件

| 参数 | 值 |
|------|----|
| `X-API-Key` | `sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36` |
| `X-User-Id` | 调用方的 Qualcomm NTID，如 `hanwzhan`，**不带 @qualcomm.com** |
| Base URL | `http://10.47.202.30:8080` |

---

## 第一步：创建会话

```bash
curl -X POST 'http://10.47.202.30:8080/api/v1/sessions' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36' \
  -H 'X-User-Id: hanwzhan' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

返回的 `data.id` 即为 `session_id`，同一个 session 可以多轮复用。

---

## 第二步：发起问答

### 不指定知识库（自动搜索 usid 有权限的全部 KB）

```bash
curl -X POST 'http://10.47.202.30:8080/api/v1/knowledge-chat/{session_id}' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36' \
  -H 'X-User-Id: hanwzhan' \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "你的问题",
    "agent_id": "builtin-quick-answer"
  }'
```

### 指定知识库（只搜指定 KB，无权限的会被自动过滤）

```bash
curl -X POST 'http://10.47.202.30:8080/api/v1/knowledge-chat/{session_id}' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36' \
  -H 'X-User-Id: hanwzhan' \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "你的问题",
    "agent_id": "builtin-quick-answer",
    "knowledge_base_ids": ["kb-00000001"]
  }'
```

### 响应格式（SSE 流）

```
data: {"response_type":"references", ...}                          ← 检索到的引用来源
data: {"response_type":"answer", "content":"回答内容...", "done":false}
data: {"response_type":"answer", "content":"", "done":true}        ← 结束标志
```

### 错误码

| 状态码 | 原因 |
|--------|------|
| `401` | 没有传 `X-User-Id` header |
| `403` | usid 对所有指定的 KB 都没有权限 |

---

## 查询 usid 有权限的知识库

```bash
curl 'http://10.47.202.30:8080/api/v1/permissions/searchable-kbs?usid=hanwzhan' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36'
```

返回示例：

```json
{
  "data": [
    { "kb_id": "kb-00000001", "name": "产品文档",   "source": "acl",    "role": "normal" },
    { "kb_id": "kb-00000002", "name": "公共知识库", "source": "common" },
    { "kb_id": "kb-00000003", "name": "运维手册",   "source": "acl",    "role": "admin"  }
  ]
}
```

`source` 含义：

| source | 说明 |
|--------|------|
| `acl` | 管理员显式授权；`role` 为 `admin`（可管理成员）或 `normal`（只读） |
| `common` | 公共 KB，所有 usid 均可搜索 |
| `super` | 该 usid 是 super_user，可搜索所有 KB |

返回的 `kb_id` 可直接用作 `knowledge_base_ids` 的值。

---

## 查询 usid 创建的会话列表

```bash
curl 'http://10.47.202.30:8080/api/v1/sessions?usid=hanwzhan' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36'
```

支持分页和关键字搜索：

```bash
curl 'http://10.47.202.30:8080/api/v1/sessions?usid=hanwzhan&page=1&page_size=20&keyword=产品' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36'
```

返回示例：

```json
{
  "success": true,
  "data": [
    { "id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", "title": "关于产品的问题", "created_at": "2026-08-26T10:00:00Z" },
    { "id": "yyyyyyyy-yyyy-yyyy-yyyy-yyyyyyyyyyyy", "title": "新会话",         "created_at": "2026-08-26T09:00:00Z" }
  ],
  "total": 2,
  "page": 1,
  "page_size": 20
}
```

> **注意**：不传 `usid` 时 API Key 调用返回空列表，必须指定 `usid` 才能查到数据。

---

## 复用旧 Session（多轮对话）

如果同一个 usid 已有历史会话，可以复用旧 session 继续对话，LLM 会自动加载历史上下文。

### 第一步：查找该 usid 的历史 session

```bash
curl 'http://10.47.202.30:8080/api/v1/sessions?usid=hanwzhan&page=1&page_size=20' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36'
```

从返回的列表里选一个 `id` 作为 `session_id`。

### 第二步：拉取该 session 的历史消息

```bash
curl 'http://10.47.202.30:8080/api/v1/messages/{session_id}/load' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36'
```

返回该 session 内所有历史消息，可展示给用户作为上下文。

### 第三步：在旧 session 里继续发问

```bash
curl -X POST 'http://10.47.202.30:8080/api/v1/knowledge-chat/{session_id}' \
  -H 'X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36' \
  -H 'X-User-Id: hanwzhan' \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "继续上面的问题……",
    "agent_id": "builtin-quick-answer"
  }'
```

LLM 会自动从数据库重建多轮对话历史，**不需要**在请求体里手动传历史消息。
