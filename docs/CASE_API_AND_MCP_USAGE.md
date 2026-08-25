# Case API、MCP 检索与部署说明

本文说明如何把支持 Case 写入指定的 WeKnora 知识库，并通过现有 Search Chunk MCP 进行语义检索和结构化过滤。

当前部署计划使用的 Case 知识库 ID：

```text
47114369-0c18-4ba0-a03e-a2c2693e117a
```

该知识库是手工创建的，数据库迁移不会创建它。部署前必须确认它存在于 API Key 对应的租户中；其他环境应替换成各自的知识库 ID。

## 1. 数据处理方式

每个 Case 对应一条普通的 WeKnora Knowledge：

1. `Summary` 作为主要语义内容；
2. Case Number、Subject、Chipset、Problem Area、Change Request、Case Date 和 Case URL 会写入可检索 Markdown；
3. 结构化属性同时保存到关系数据库，用于精确过滤；
4. Knowledge 继续使用原有 Chunk、Embedding 和 Milvus 索引流程；
5. 不直接写 Milvus，也不创建新的 Milvus collection；
6. `Comments` 不写入 Knowledge、Chunk 或 Milvus，应只保留在每日脚本生成的本地原始 JSON 中；
7. `Description` 和 `extra_attributes` 会保存在关系数据库，但不会写入可向量检索的 Markdown。

这不会改变已有 PDF、Word、Markdown 等文件的上传和索引方式。不传 Case `filters` 时，两个 Search Chunk MCP 工具保持原有行为。

## 2. Case Upsert API

### 2.1 接口

```http
PUT /api/v1/knowledge-bases/{kb_id}/cases/{case_number}
```

该接口按当前租户和 Case Number 幂等写入：

- 第一次请求创建 Case 映射和普通 Knowledge；
- 搜索内容发生变化时，更新 Knowledge 并重新进入现有异步索引流程；
- 搜索内容没有变化时，不重复向量化；
- 同一个租户中的 Case Number 不能从一个知识库移动到另一个知识库。

### 2.2 认证和权限

请求需要以下 Header：

```http
Content-Type: application/json
X-API-Key: <WEKNORA_API_KEY>
```

也可以使用系统已有的 Bearer 认证。默认启用 RBAC 时，当前路由同时执行知识库 Owner/Admin 检查和写权限检查，因此 Bearer 调用者必须是知识库所有者或租户 Admin，并具备目标知识库写权限；仅被共享为 Editor 但不是所有者/Admin 的账号会被拒绝。若部署显式关闭 RBAC，这些权限守卫会放行并记录日志，行为会有所不同。

租户 API Key 在认证中被授予该租户的 Admin 角色，因此可写入该租户内任意知识库，不受单个知识库 ACL 限制。应把它视为租户级高权限凭据。`X-User-Id` 只用于 API Key 调用时透传业务用户身份，Bearer 模式下身份来自 JWT；它不会把 Case Upsert API Key 的写权限收窄为该业务用户的权限。

不要把 API Key 写入脚本源码、Markdown 或镜像；应通过环境变量或服务器的 secret 管理方式提供。

### 2.3 请求字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `summary` | string | 是 | AI 生成的 Case 总结，主要语义检索内容 |
| `subject` | string | 否 | Case 标题 |
| `description` | string | 否 | 保存在关系数据库中，但不进入向量内容 |
| `problem_area_1` | string | 否 | 精确过滤字段，同时写入可检索 Markdown |
| `problem_area_2` | string | 否 | 同上 |
| `problem_area_3` | string | 否 | 同上 |
| `case_url` | string | 否 | Case 链接 |
| `case_date` | string | 否 | 严格使用 `YYYY-MM-DD` |
| `chipsets` | string[] | 否 | 可包含多个 Chipset；会去空、去重并排序 |
| `change_request_numbers` | string[] | 否 | 可包含多个 Change Request Number |
| `extra_attributes` | object | 否 | 为未来结构化字段预留；当前不进入向量内容 |

不要发送 `comments` 字段。

字段长度限制：Case Number 最多 64 字符、Subject 最多 1000 字符、每级 Problem Area 最多 255 字符、Chipset 最多 128 字符、Change Request Number 最多 64 字符；此外自动生成的 Knowledge 标题最多 255 字符，因此较长的 Case Number + Subject 组合可能在 Subject 达到 1000 字符前就被拒绝。

### 2.4 curl 示例

```bash
export WEKNORA_BASE_URL='http://10.47.202.30:8080'
export WEKNORA_CASE_KB_ID='47114369-0c18-4ba0-a03e-a2c2693e117a'
export WEKNORA_API_KEY='<从环境或 secret 管理器读取>'

curl --request PUT \
  "${WEKNORA_BASE_URL}/api/v1/knowledge-bases/${WEKNORA_CASE_KB_ID}/cases/8538861" \
  --header "X-API-Key: ${WEKNORA_API_KEY}" \
  --header 'Content-Type: application/json' \
  --data '{
    "subject": "Modem crash after network recovery",
    "description": "Optional source description; not embedded",
    "summary": "The crash was caused by a race during network recovery. The fix serializes the state transition.",
    "problem_area_1": "Modem",
    "problem_area_2": "Crash",
    "problem_area_3": "Recovery",
    "case_url": "https://example.invalid/cases/8538861",
    "case_date": "2026-08-05",
    "chipsets": ["QCM6490", "QCS8550"],
    "change_request_numbers": ["3901518"],
    "extra_attributes": {
      "source_system": "ODS"
    }
  }'
```

### 2.5 成功响应

```json
{
  "success": true,
  "data": {
    "case_number": "8538861",
    "knowledge_base_id": "47114369-0c18-4ba0-a03e-a2c2693e117a",
    "knowledge_id": "<knowledge UUID>",
    "action": "created",
    "parse_status": "pending"
  }
}
```

`action` 含义：

| 值 | 含义 |
| --- | --- |
| `created` | 新建了 Case 和对应 Knowledge |
| `updated` | 已有 Case 的可检索内容发生变化，Knowledge 已更新并重新索引；或未发布/失败的 Knowledge 被重新发布 |
| `unchanged` | 可检索内容没有变化，未重复索引；`description`、`case_url`、`extra_attributes` 等非检索标量字段仍可被更新 |

`parse_status` 是异步处理状态。常见流程为 `pending` → `processing` → `finalizing` → `completed`。API 返回成功只表示写入和索引任务提交成功，不表示向量化已经完成。

### 2.6 常见错误

- `400`：缺少 Summary、日期格式错误、JSON 无效或字段超长；
- `401`：未认证；
- `403`：启用 RBAC 时，没有目标知识库写权限，或 Bearer 调用者未通过 Owner/Admin 检查；
- `404`：知识库不存在（根据调用者角色和前置权限检查，也可能先返回 `403`）；
- `409`：同租户下该 Case Number 已绑定到其他知识库；
- `500`：未知服务端错误；
- `503`：权限守卫查询知识库或所有者时发生暂时性服务错误。

## 3. 每日脚本配置

`C:\CODE\ModemCEBOT\cebot\api\script\daily_case_v2.py` 使用以下环境变量调用 Case API：

```text
WEKNORA_BASE_URL=http://10.47.202.30:8080
WEKNORA_CASE_KB_ID=47114369-0c18-4ba0-a03e-a2c2693e117a
WEKNORA_API_KEY=<API Key>
WEKNORA_TIMEOUT=30
```

脚本继续负责 Case 获取、LLM 总结、本地完整 JSON 保存、重试、状态统计和通知。只有发送到 WeKnora 的投影排除 Comments，本地原始 JSON 不受影响。

## 4. Search Chunk MCP 的 Case 过滤

以下两个现有 MCP 工具都支持可选的 `filters`：

- `search_chunks_from_single_kb`
- `search_chunks_from_all_kb`

这两个工具由 Go app 的 Streamable HTTP MCP 端点提供：

```text
http://<app-host>:8080/mcp
```

不要连接 Compose 中用途不同的 Python `mcp` sidecar 来调用这两个工具。

使用 Bearer JWT 时，用户身份来自 JWT。使用 API Key 时可以通过 `X-User-Id` 透传实际业务用户；建议显式传递，否则中间件会选择租户用户或合成系统身份，搜索权限范围可能与预期不同。服务只会搜索最终身份有权访问的知识库。

### 4.1 支持的过滤字段

```json
{
  "case_number": "8538861",
  "chipsets": ["QCM6490", "QCS8550"],
  "problem_area_1": ["Modem"],
  "problem_area_2": ["Crash"],
  "problem_area_3": ["Recovery"],
  "change_request_numbers": ["3901518"],
  "case_date_from": "2026-08-01",
  "case_date_to": "2026-08-31"
}
```

日期必须使用 `YYYY-MM-DD`，且 `case_date_from` 不能晚于 `case_date_to`。

### 4.2 单知识库示例

```json
{
  "kb_id": "47114369-0c18-4ba0-a03e-a2c2693e117a",
  "query": "network recovery crash root cause",
  "limit": 10,
  "filters": {
    "chipsets": ["QCM6490"],
    "problem_area_1": ["Modem"],
    "case_date_from": "2026-08-01",
    "case_date_to": "2026-08-31"
  }
}
```

### 4.3 所有有权知识库示例

```json
{
  "query": "change request fix and validation",
  "limit": 10,
  "filters": {
    "change_request_numbers": ["3901518"],
    "problem_area_2": ["Crash", "Stability"]
  }
}
```

### 4.4 过滤语义

- 不同字段之间使用 **AND**；
- 同一数组字段中的多个值使用 **OR**；
- Case Number、Chipset、Problem Area 和 Change Request Number 都是数据库精确匹配，并区分大小写；写入和查询时应统一大小写及格式；
- 日期范围包含边界；
- `filters` 先从关系数据库筛出 Case 对应的 Knowledge ID，再进入原有混合检索和排序；
- 同时传 `doc_ids` 和 `filters` 时取二者交集；
- 没有符合过滤条件的 Case 时直接返回 0 条，不会退化成不受限搜索；
- 一旦传入非空 Case `filters`，候选内容仅来自具有 Case 结构化记录的 Knowledge；普通 PDF、Word、Markdown 不会误入结果；
- 不传 `filters` 时，仍按原来的方式搜索普通文档和 Case。

## 5. Swagger 更新机制

Swagger UI 不是从 Gin 路由运行时动态生成的。它读取编译进应用的以下生成文件：

```text
docs/docs.go
docs/swagger.json
docs/swagger.yaml
```

因此，仅执行 `docker compose build app` **不会自动重新生成 Swagger**。正确顺序是：

```bash
# 第一次使用时安装 swag
make install-swagger

# 根据 Handler 注释更新生成文件
make docs

# 确认新路径已生成后再构建镜像
# /knowledge-bases/{id}/cases/{case_number}
docker compose build app
```

本次 Case Handler 已添加 Swagger 注释，并已重新生成上述三个文件。新镜像构建后会包含新接口文档。

另外，当前 Compose 默认值为：

```text
GIN_MODE=release
```

代码在 `release` 模式下不会注册 `/swagger/*` 路由。如果服务器当前能打开 `http://10.47.202.30:8080/swagger/index.html`，说明实际部署覆盖了该默认值，或者运行的版本与当前代码不同。若需要在部署后继续查看 Swagger，应显式设置非 `release` 模式；生产环境是否开放 Swagger 应按安全策略决定。

## 6. 构建、打包和部署 app 镜像

本次只修改后端 app、数据库迁移和后端 Swagger，不需要重建 frontend 或 docreader。可以只构建 `app`，但必须先生成 Swagger，并确保新迁移文件包含在镜像中。

建议使用唯一版本号，不要依赖可能残留旧镜像的 `latest`。以下仅为操作步骤，执行生产部署前应先备份数据库并安排回滚窗口。

### 6.1 在构建机生成文档并构建

`WEKNORA_VERSION` 是 Compose 中 app、frontend、docreader 和 sandbox 共用的版本变量。构建 app 时可以临时设置它来生成指定 tag，但不要把 `WEKNORA_VERSION=case-v1` 持久写入服务器 `.env`，否则其他服务以及 app 动态启动的 sandbox 也会尝试使用并不存在的 `case-v1` 镜像。

Linux/macOS：

```bash
make docs

WEKNORA_VERSION=case-v1 docker compose build app

docker image inspect wechatopenai/weknora-app:case-v1 >/dev/null
docker save -o weknora-app-case-v1.tar wechatopenai/weknora-app:case-v1
```

Windows PowerShell：

```powershell
make docs

$env:WEKNORA_VERSION = "case-v1"
docker compose build app
Remove-Item Env:WEKNORA_VERSION

docker image inspect wechatopenai/weknora-app:case-v1
docker save -o weknora-app-case-v1.tar wechatopenai/weknora-app:case-v1
```

只需要传输新生成的 `weknora-app-case-v1.tar`。不要把 API Key 打进镜像。

### 6.2 部署前检查

1. 备份 PostgreSQL 数据库，并验证备份文件可读；
2. 保存服务器当前 app 镜像的 tag 或 image ID，便于回滚；
3. 确认服务器上的 `docker-compose.yml` 与本次代码兼容；
4. 不要修改服务器 `.env` 中供全部服务共用的 `WEKNORA_VERSION`；本次通过一个临时 Compose override 文件只覆盖 app 的镜像 tag；
5. 确认 `AUTO_MIGRATE` 没有设置为 `false`，或准备手工执行迁移；
6. 当前 Compose 默认 `AUTO_RECOVER_DIRTY=true`，遇到 dirty migration 时会尝试把版本强制回退一版并重试。生产部署若不接受自动恢复，应在部署前设置 `AUTO_RECOVER_DIRTY=false`，并对 dirty 状态进行人工核查；
7. 确认服务器磁盘空间足够加载镜像和执行数据库备份。

### 6.3 在服务器加载并重建 app 容器

```bash
docker load -i weknora-app-case-v1.tar

docker image inspect wechatopenai/weknora-app:case-v1 >/dev/null

# 临时文件只覆盖 app 镜像，不改变共享的 WEKNORA_VERSION
cat > docker-compose.case-app.yml <<'EOF'
services:
  app:
    image: wechatopenai/weknora-app:case-v1
EOF

docker compose -f docker-compose.yml -f docker-compose.case-app.yml \
  up -d --no-deps --force-recreate app

docker compose -f docker-compose.yml -f docker-compose.case-app.yml ps app
docker compose -f docker-compose.yml -f docker-compose.case-app.yml logs --tail=200 app
```

只要 app 需要继续运行 `case-v1`，后续针对 app 的 Compose 命令都应带上该 override 文件。这样 frontend、docreader 和 app 动态启动的 sandbox 仍使用服务器 `.env` 中原有的共享版本。不要使用 `WEKNORA_VERSION=case-v1 docker compose ...` 启动 app，因为该变量还会改写 app 配置中的 sandbox 镜像 tag。

应用镜像包含 `migrations/versioned/000063_cases.*.sql`。默认情况下，app 启动时会自动执行待处理迁移。需要注意：当前实现中迁移失败只记录警告，应用仍会继续启动，所以不能只看容器是否为 `healthy`；必须检查日志并确认 migration version 已到 `63`、没有 dirty 状态或 migration error。

生产 Compose 使用 PostgreSQL；`migrations/sqlite/000063_cases.*.sql` 用于已有 SQLite 安装升级。

### 6.4 部署后验证

建议按以下顺序验证：

1. `GET /health` 返回 `200`；
2. app 日志显示迁移成功且数据库版本为 `63`；
3. 数据库中存在 `cases`、`case_chipsets`、`case_change_requests` 三张表；
4. Swagger 开启时，新路径出现在 `/swagger/index.html`；
5. 使用一个测试 Case 调用 Upsert API，确认第一次为 `created`；
6. 等待对应 Knowledge 的 `parse_status` 变为 `completed`；
7. 相同请求再次调用，确认返回 `unchanged`；
8. 修改 Summary 后再次调用，确认返回 `updated`；
9. 分别用无过滤搜索、Chipset 过滤、Problem Area 过滤和日期范围过滤验证 MCP；
10. 确认原有 PDF/Word/Markdown 搜索仍正常。

Case 关系记录与 Knowledge 更新目前不处于同一个数据库事务中：极少数情况下，如果 Knowledge 已更新而 Case 属性更新失败，语义内容和精确过滤可能暂时不一致。此时应根据 app 日志修复故障后，用同一 Case Number 重试 Upsert；创建阶段发布失败时也可通过重试恢复草稿 Knowledge。

## 7. 回滚注意事项

- 应用回滚：不要修改共享的 `.env` 版本；使用旧 app tag 临时覆盖并重新创建 app 容器，例如 `WEKNORA_VERSION=<旧版本> docker compose up -d --no-deps --force-recreate app`；
- 数据库回滚：不要在已有 Case 数据时直接执行 down migration，否则会删除三张 Case 表及其中数据；
- 如果新版本已经写入 Case 数据，优先保留数据库表，仅回滚应用镜像；
- 只有确认要永久撤销 Case 功能且已单独备份 Case 数据时，才考虑执行 `000063_cases.down.sql`；
- 不需要对 Milvus 执行 schema 迁移或创建新 collection。
