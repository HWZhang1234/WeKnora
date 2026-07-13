# WeKnora REST API 完整指南（CEBOT 集成用）

**Base URL:** `http://10.47.202.30:8080/api/v1`  
**认证:** `X-API-Key: sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36`

---

## 1. 创建知识库

```bash
POST /knowledge-bases
Content-Type: application/json
X-API-Key: sk-xxx

{
  "name": "CEBOT知识库",
  "description": "自动创建",
  "embedding_model_id": "<embedding模型ID>"
}
```

**响应 (201):**
```json
{
  "id": "uuid-of-new-kb",
  "name": "CEBOT知识库",
  "description": "自动创建",
  "knowledge_count": 0
}
```

---

## 2. 上传文件

```bash
POST /knowledge-bases/:kb_id/knowledge/file
Content-Type: multipart/form-data
X-API-Key: sk-xxx

file=@document.pdf           # 必填，文件 (最大50MB)
custom_filename=自定义名称    # 可选
tags=tag1,tag2               # 可选
```

**响应 (200):**
```json
{
  "id": "doc-uuid",
  "file_name": "document.pdf",
  "file_size": 1048576,
  "parse_status": "pending"
}
```

---

## 3. 获取文档列表

```bash
GET /knowledge-bases/:kb_id/knowledge?page=1&page_size=20&status=completed&keyword=PCIe
X-API-Key: sk-xxx
```

**查询参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `page` | 1 | 页码 |
| `page_size` | 20 | 每页数量 (最大100) |
| `status` | - | 过滤: pending/processing/completed/failed |
| `keyword` | - | 按文件名搜索 |

**响应 (200):**
```json
{
  "items": [
    {
      "id": "doc-uuid",
      "file_name": "document.pdf",
      "file_size": 1048576,
      "parse_status": "completed",
      "chunk_count": 42,
      "created_at": "2026-06-30T10:00:00Z"
    }
  ],
  "total": 97,
  "page": 1,
  "page_size": 20
}
```

---

## 4. 下载文档

```bash
GET /knowledge/:doc_id/download
X-API-Key: sk-xxx
```

**响应:** 二进制文件流，带 `Content-Disposition` 头

```bash
# curl 示例
curl -o output.pdf \
  -H "X-API-Key: sk-xxx" \
  http://10.47.202.30:8080/api/v1/knowledge/DOC_ID/download
```

---

## 5. 删除文件

```bash
DELETE /knowledge/:doc_id
X-API-Key: sk-xxx
```

**响应 (200):**
```json
{
  "message": "knowledge deleted successfully"
}
```

> 注：异步删除，会同时清理关联的向量数据

---

## 6. 删除知识库

```bash
DELETE /knowledge-bases/:kb_id
X-API-Key: sk-xxx
```

**响应 (200):**
```json
{
  "message": "knowledge base deleted successfully"
}
```

> 注：会级联删除所有文档和向量数据

---

## CEBOT 集成总结

```
┌────────────────────────────────────────────────────┐
│  CEBOT 调用 WeKnora 的完整 API 清单               │
├──────────────┬─────────────────────────────────────┤
│ 创建知识库   │ POST   /knowledge-bases             │
│ 删除知识库   │ DELETE /knowledge-bases/:id         │
│ 上传文件     │ POST   /knowledge-bases/:id/knowledge/file │
│ 删除文件     │ DELETE /knowledge/:id               │
│ 下载文件     │ GET    /knowledge/:id/download      │
│ 文档列表     │ GET    /knowledge-bases/:id/knowledge│
├──────────────┼─────────────────────────────────────┤
│ RAG 问答     │ MCP    chat / agent_invoke          │
│ 检索文档     │ MCP    search_chunks                │
│ 查看知识库   │ MCP    kb_list / kb_view            │
└──────────────┴─────────────────────────────────────┘
```

**原则：** 数据管理走 **REST API**，AI 能力走 **MCP**。

---

## MCP 连接配置

```json
{
  "WeKnora": {
    "type": "http",
    "url": "http://10.47.202.30:8080/mcp",
    "headers": {
      "X-API-Key": "sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36"
    }
  }
}
```

### MCP 可用工具列表

| 工具名 | 功能 |
|--------|------|
| `kb_list` | 列出所有知识库 |
| `kb_view` | 查看知识库详情 |
| `doc_list` | 列出知识库中的文档 |
| `doc_view` | 查看文档详情 |
| `doc_download` | 下载文档内容 |
| `chunk_list` | 查看文档分块 |
| `search_chunks` | 混合检索（向量+关键词） |
| `chat` | RAG 问答（指定知识库） |
| `agent_invoke` | 调用智能体问答 |
| `agent_list` | 列出可用智能体 |

---

## MCP 问答使用指南

### 场景一：针对某个知识库问答（chat）

使用 `chat` 工具，传入指定的 `kb_id`：

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "id": 1,
  "params": {
    "name": "chat",
    "arguments": {
      "kb_id": "968c86fa-ca5e-4470-8f17-248d5b639910",
      "query": "PCIe D3cold电源状态管理的要求是什么？"
    }
  }
}
```

**多轮对话**：首次调用返回 `session_id`，后续传入即可保持上下文：

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "id": 2,
  "params": {
    "name": "chat",
    "arguments": {
      "kb_id": "968c86fa-ca5e-4470-8f17-248d5b639910",
      "query": "还有哪些具体的实施步骤？",
      "session_id": "f52c0176-d3bf-4a76-9b9e-11c5e6c5fde2"
    }
  }
}
```

**响应格式：**
```json
{
  "answer": "完整的回答文本...",
  "references": [
    {
      "knowledge_id": "doc-uuid",
      "knowledge_title": "文档名称.pdf",
      "content": "引用的原文片段...",
      "score": 0.85
    }
  ],
  "session_id": "f52c0176-d3bf-4a76-9b9e-11c5e6c5fde2"
}
```

---

### 场景二：跨全部知识库问答（agent_invoke）

使用 `agent_invoke` 工具，调用预配置好的智能体（智能体已绑定多个/全部知识库）：

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "id": 1,
  "params": {
    "name": "agent_invoke",
    "arguments": {
      "agent_id": "builtin-quick-answer",
      "query": "PCIe D3cold电源状态管理的要求是什么？"
    }
  }
}
```

**多轮对话**：同样支持 `session_id`：

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "id": 2,
  "params": {
    "name": "agent_invoke",
    "arguments": {
      "agent_id": "builtin-quick-answer",
      "query": "具体实施步骤有哪些？",
      "session_id": "返回的session_id"
    }
  }
}
```

---

### 可用智能体列表

| 智能体名称 | ID | 描述 |
|-----------|-----|------|
| 快速问答 | `builtin-quick-answer` | 基于知识库的 RAG 问答，快速准确地回答问题 |
| 智能推理 | `builtin-smart-reasoning` | ReAct 推理框架，支持多步思考与工具调用 |
| 维基问答 | `builtin-wiki-researcher` | 专注于在 Wiki 知识库中回答问题的智能体 |
| 数据分析师 | `builtin-data-analyst` | 专业的数据分析智能体，支持对 CSV/Excel 文件进行 SQL 查询和统计分析 |

> 💡 获取最新智能体列表：调用 MCP `agent_list` 工具

---

### CEBOT 选择逻辑

| 用户选择 | MCP 工具 | 参数 |
|---------|----------|------|
| 指定某个知识库 | `chat` | `kb_id` = 对应知识库 ID |
| 全部知识库 | `agent_invoke` | `agent_id` = 绑定了所有知识库的智能体 ID |
| 指定多个知识库 | `agent_invoke` | `agent_id` = 绑定了目标知识库的智能体 ID |

---

## SSE 流式问答接口（逐字输出）

> 💡 这是 WeKnora 原有接口，网页前端用的就是它。CEBOT 直接调用即可，**无需额外部署**。

### 前置步骤：创建 Session

流式接口需要先有一个 `session_id`。可以通过 REST API 创建：

```bash
POST /sessions
Content-Type: application/json
X-API-Key: sk-xxx

{
  "title": "用户问题的前50字"
}
```

**响应 (201):**
```json
{
  "id": "session-uuid",
  "title": "用户问题的前50字"
}
```

---

### 知识库问答（流式）

针对指定知识库的 RAG 问答，逐字流式返回：

```bash
POST /knowledge-chat/{session_id}
Content-Type: application/json
X-API-Key: sk-xxx
X-Request-ID: unique-request-id

{
  "query": "PCIe D3cold电源状态管理的要求是什么？",
  "knowledge_base_ids": ["968c86fa-ca5e-4470-8f17-248d5b639910"]
}
```

**请求参数：**

| 字段 | 必填 | 说明 |
|------|------|------|
| `query` | ✅ | 用户问题 |
| `knowledge_base_ids` | ❌ | 知识库 ID 数组，不传则用 session 默认 |
| `knowledge_ids` | ❌ | 指定文档 ID 数组 |
| `web_search_enabled` | ❌ | 是否启用网络搜索 |
| `summary_model_id` | ❌ | 覆盖模型 |
| `channel` | ❌ | 来源标记："web"\|"api"\|"im" |

---

### 智能体问答（流式）

调用智能体（支持多知识库、工具调用、推理）：

```bash
POST /agent-chat/{session_id}
Content-Type: application/json
X-API-Key: sk-xxx

{
  "query": "你的问题",
  "agent_id": "builtin-quick-answer"
}
```

**请求参数：**

| 字段 | 必填 | 说明 |
|------|------|------|
| `query` | ✅ | 用户问题 |
| `agent_id` | ❌ | 智能体 ID（不传则用默认模式） |
| `knowledge_base_ids` | ❌ | 覆盖智能体绑定的知识库 |
| `agent_enabled` | ❌ | 是否启用 agent 推理 |

---

### SSE 响应格式

**响应头：**
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

**事件流示例：**
```
event: message
data: {"response_type":"references","knowledge_references":[{"knowledge_id":"doc-id","knowledge_title":"文档.pdf","content":"相关片段...","score":0.85}],"done":false}

event: message
data: {"response_type":"answer","content":"根据","done":false}

event: message
data: {"response_type":"answer","content":"检索到的文档","done":false}

event: message
data: {"response_type":"answer","content":"，PCIe端点必须...","done":false}

event: message
data: {"response_type":"answer","content":"","done":true,"session_id":"xxx","assistant_message_id":"msg-uuid"}
```

**response_type 类型：**

| response_type | 说明 |
|---------------|------|
| `references` | 知识库检索结果（含引用列表） |
| `answer` | 回答文本片段（`done:true` 表示结束） |
| `thinking` | 智能体思考过程 |
| `tool_call` | 工具调用通知 |
| `tool_result` | 工具返回结果 |
| `session_title` | 自动生成的会话标题 |
| `error` | 错误信息 |

---

### 停止生成

```bash
POST /sessions/{session_id}/stop
X-API-Key: sk-xxx
```

---

### CEBOT 完整调用流程

```
1. 创建 Session  →  POST /sessions  →  得到 session_id
2. 发起流式问答  →  POST /knowledge-chat/{session_id}
                    或 POST /agent-chat/{session_id}
3. 逐行读取 SSE  →  解析 event: message / data: {...}
4. 拼接 answer   →  response_type="answer" 的 content 逐个拼接
5. 结束标志      →  done=true
6. 停止生成(可选) →  POST /sessions/{session_id}/stop
```

---

## CEBOT 最终架构总结

```
┌─────────────────────────────────────────────────────┐
│                      CEBOT                           │
├─────────────────────────────────────────────────────┤
│                                                     │
│  数据管理 ──────→ REST API (非流式)                  │
│    POST   /knowledge-bases           创建知识库      │
│    DELETE  /knowledge-bases/:id       删除知识库      │
│    POST   /knowledge-bases/:id/knowledge/file 上传   │
│    DELETE  /knowledge/:id            删除文件         │
│    GET    /knowledge/:id/download    下载文件         │
│    GET    /knowledge-bases/:id/knowledge  文档列表   │
│                                                     │
│  AI 问答 ───────→ SSE 接口 (流式，逐字输出)          │
│    POST  /knowledge-chat/{session_id}  知识库问答    │
│    POST  /agent-chat/{session_id}      智能体问答    │
│    POST  /sessions/{session_id}/stop   停止生成      │
│                                                     │
│  辅助查询 ──────→ MCP (非流式，一次性返回)            │
│    kb_list / doc_list / search_chunks              │
│    chat / agent_invoke (非流式备选)                  │
│                                                     │
└─────────────────────────────────────────────────────┘
```
