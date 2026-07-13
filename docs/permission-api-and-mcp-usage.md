# WeKnora 权限层 —— REST API 与 MCP 工具调用指南

本文档描述本次新增的 **usid 权限层**：一组权限管理 REST 接口，以及经过权限改造的
MCP 工具（`search_chunks` / `chat`）。所有示例已在生产环境
`http://10.47.202.30:8080` 上实测通过（2026-07-07）。

---

## 1. 概念模型

权限系统围绕 **usid（业务用户 ID）** 展开，由三张表构成：

| 概念 | 表 | 含义 |
|------|-----|------|
| **super_user** | `super_user` | 超级用户，可访问 **全部** 知识库，可管理所有权限（super_user / common_kb / 任意 KB 的 kb_acl）。 |
| **kb_acl** | `kb_acl` | 某个知识库的成员名单。每条记录 = (kb_id, usid, role)，role ∈ `admin` \| `normal`。KB 的 `admin` 可管理该 KB 的成员名单。 |
| **common_kb** | `common_kb` | 公共知识库。被标记为 common 的 KB，对 **任意** usid 可见（相当于全员可读）。 |

### usid 的可访问 KB 范围（search scope）

```
如果 usid ∈ super_user        →  可访问【全部】KB
否则                          →  可访问 = (该 usid 在 kb_acl 中被授权的 KB)  ∪  (所有 common_kb)
```

> 引导用户（bootstrap super_user）：`hanwzhan`、`whui`、`ksa`（由 migration `000060` 初始化）。

---

## 2. 鉴权（所有接口通用）

- 所有 `/api/v1/**` 接口都受全局 API-Key 中间件保护。
- **必须**带请求头：`X-API-Key: <key>`（注意：不是 `Authorization: Bearer`）。
- 缺失或错误的 key → `HTTP 401 {"error":"Unauthorized: missing authentication"}`。

权限接口在 API-Key 之上，还要求请求自报一个 **`operator_usid`**（操作者身份），
服务端会用它校验权限：

- super_user 级操作：`operator_usid` 必须 ∈ super_user，否则 `403`。
- kb_acl 操作：`operator_usid` 必须是该 KB 的 admin **或** super_user，否则 `403`。
- common_kb 读：任意认证用户可读；写（增删）需 super_user。

### 统一响应格式

成功：
```json
{ "success": true, "data": ... }        // data 视接口而定，写操作可能只有 {"success":true}
```

失败：
```json
{ "success": false, "error": { "code": 1000, "message": "...", "details": null } }
```

| code | HTTP | 含义 |
|------|------|------|
| 1000 | 400 | 参数错误（如缺 `operator_usid` / `kb_id`，role 非法） |
| 1002 | 403 | 无权限（operator 不是 super_user / 不是该 KB 的 admin） |

---

## 3. 权限管理 REST API

Base URL：`http://<host>:8080/api/v1`

> Swagger（`/swagger/index.html`）中**暂未列出**这些接口，因为静态 spec 未重新生成（`swag init`）；接口本身已上线可用，以本文档为准。

### 3.1 kb_acl —— 知识库成员管理

#### 添加 / 更新成员　`POST /permissions/kb-acl`
操作者需为该 KB 的 admin 或 super_user。

Body：
```json
{
  "operator_usid": "hanwzhan",
  "kb_id": "968c86fa-ca5e-4470-8f17-248d5b639910",
  "usid": "alice",
  "role": "admin"          // "admin" | "normal"
}
```
```bash
curl -X POST http://10.47.202.30:8080/api/v1/permissions/kb-acl \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"operator_usid":"hanwzhan","kb_id":"<KB_ID>","usid":"alice","role":"admin"}'
# -> {"success":true}
```

#### 移除成员　`DELETE /permissions/kb-acl`
```bash
curl -X DELETE http://10.47.202.30:8080/api/v1/permissions/kb-acl \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"operator_usid":"hanwzhan","kb_id":"<KB_ID>","usid":"alice"}'
# -> {"success":true}
```

#### 查询　`GET /permissions/kb-acl`
两种模式（二选一，靠 query 参数区分）：

**模式 A — 列出某 KB 的成员**（需 operator 为该 KB admin 或 super_user）：
**这就是"获取某知识库有哪些 admin / normal usid"的接口。**
```bash
curl -H "X-API-Key: $KEY" \
  "http://10.47.202.30:8080/api/v1/permissions/kb-acl?operator_usid=hanwzhan&kb_id=<KB_ID>"
```
```json
{"success":true,
 "data":[
   {"id":2,"kb_id":"<KB_ID>","usid":"alice","role":"admin",
    "created_at":"2026-07-07T18:21:10+08:00","updated_at":"2026-07-07T18:21:10+08:00"}
 ],
 "display_source":{"admins":["group:groupA"],"normals":["usid:PEIQIANG"]}}
```
> `data` 是**真实 usid 名单**（权限判定的依据）；`display_source` 是 batch 时存的**原始组/人标签**
> （纯展示，无则为 `null`，见 §3.1 `display_source`）。前端用它把一堆 usid 还原成组显示。

**模式 B — 列出某 usid 可访问的 KB**（operator 为 super_user，或查询自己）：
```bash
curl -H "X-API-Key: $KEY" \
  "http://10.47.202.30:8080/api/v1/permissions/kb-acl?operator_usid=alice&usid=alice"
```

> 未提供 `kb_id` 也未提供 `usid` → `400`。

#### 批量全量替换名单　`POST /permissions/kb-acl/batch`

**推荐用于"建库授权"和"成员管理面板保存"。** 这是**全量替换**语义：每次保存时把该 KB
**完整的** admins / normals 名单传上来，后端把 KB 的 acl 对齐成你传的样子——名单里没有的成员会被删除，名单里的成员被插入/更新。前端无需自己计算增量差异。

**权限（含首建授权 bootstrap，2026-07-08 更新）**：
- **该 KB 还没有任何成员时（kb_acl 为空，即刚建出来的新库）**：**任意认证调用方**都可做首次授权，
  `operator_usid` 传谁都行——这就是"建库对话框点 Create/Save"的场景，建库者在这一步选定初始 admins。
  这样解决了"先有鸡还是先有蛋"：新库没人是 admin，若强制"必须已是该 KB admin"就永远进不去。
- **该 KB 已有成员时**：`operator_usid` 必须是该 KB 的 admin 或 super_user，否则 `403`。

Body：
```json
{
  "operator_usid": "ruizhou",
  "kb_id": "<KB_ID>",
  "admins":  ["ruizhou", "u1", "u2", "u3"],
  "normals": ["PEIQIANG", "u10", "u11"],
  "display_source": {
    "admins":  ["usid:ruizhou", "group:groupA"],
    "normals": ["usid:PEIQIANG", "group:GROUPB"]
  }
}
```
```bash
curl -X POST http://10.47.202.30:8080/api/v1/permissions/kb-acl/batch \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"operator_usid":"ruizhou","kb_id":"<KB_ID>","admins":["ruizhou","u1","u2"],"normals":["PEIQIANG","u10"]}'
```
返回替换后的完整名单 + 回显的 `display_source`（供前端刷新，无 display_source 时为 `null`）：
```json
{"success":true,
 "data":[
  {"id":10,"kb_id":"<KB_ID>","usid":"ruizhou","role":"admin","created_at":"...","updated_at":"..."},
  {"id":11,"kb_id":"<KB_ID>","usid":"u1","role":"admin","created_at":"...","updated_at":"..."},
  {"id":12,"kb_id":"<KB_ID>","usid":"PEIQIANG","role":"normal","created_at":"...","updated_at":"..."}
 ],
 "display_source":{"admins":["usid:ruizhou","group:groupA"],"normals":["usid:PEIQIANG","group:GROUPB"]}}
```

**后端处理规则（重要）**：
- **冲突消解**：同一 usid 同时出现在 `admins` 和 `normals` → 归为 **admin**（admin 权限 ⊇ normal）。
  （实现：先写 normals 再写 admins，同一 usid 被 admin 覆盖；DB 层 `UNIQUE(kb_id, usid)` 也保证一人一角色。）
- **admins 非空**：解析去重后若没有任何 admin → `400 "at least one admin is required"`（后端强制，防止 KB 变成无人可管理）。
- **原子事务**：upsert 名单内成员 + 删除名单外的旧成员，一次事务完成，不会出现半更新。
- 空白 usid 自动忽略，其余去重。

**`display_source`（可选，纯展示，2026-07-08 新增）**：
调用方在界面里选的可能是**组（group）+ 个人（usid）**，如建库对话框里 admins 填了 `ruizhou` + `groupA`、
normals 填了 `PEIQIANG` + `GROUPB`（见下方"建库对话框"）。调用方会把**组展开成组内每个 usid** 再放进
`admins`/`normals`（后端只认展开后的 usid、只用它做权限判定）。但展开后"组"这个来源标签就丢了，
`GET /permissions/kb-acl` 回来一堆 usid、还原不出组名。
- `display_source` 就是让你把**用户原始选择**（组/人的 token，如 `group:groupA`、`usid:bob`）原样传上来，
  后端**原样存、原样回显**，用于前端把名单渲染成分组视图。
- **它绝不参与权限判定**——判定永远只看展开后的 usid（`data` 里那些）。token 字符串对后端不透明，
  你自己定义格式（推荐 `group:xxx` / `usid:xxx`）。
- **不传该字段** → 完全等同旧行为，后端不动已存的 display_source。
- ⚠️ **它是"授权时快照"**：`group:groupA` 存的是保存那一刻展开的 usid；之后 groupA 在外部系统里加/减人，
  后端 usid 不会自动跟随，直到**下次有人再点 Save 重新展开**。前端显示组时建议标注"授权时快照"。

> 建库流程（对应"建库对话框"）：先 `POST /api/v1/knowledge-bases` 建库拿到 `id`；前端把每个组展开成 usid，
> 连同 `display_source` 一起调本接口——此时 kb_acl 为空，走 bootstrap 首建授权，`operator_usid` 传建库者即可。
> 后续单个增删改仍可用 `POST` / `DELETE /permissions/kb-acl`（见上），与本接口不冲突。

##### 建库对话框（外部应用调用示例）

外部应用的 Create Database 对话框里，Admins / Normal Users 都可混填**个人**和**组**：

| 字段 | 用户填的（原始） | 展开后传给后端的 `admins`/`normals` | `display_source` 里存的 |
|------|------------------|-------------------------------------|--------------------------|
| Admins | `ruizhou`（人）、`groupA`（组） | `ruizhou` + groupA 展开的所有 usid | `["usid:ruizhou","group:groupA"]` |
| Normal Users | `PEIQIANG`（人）、`GROUPB`（组） | `PEIQIANG` + GROUPB 展开的所有 usid | `["usid:PEIQIANG","group:GROUPB"]` |

回显时：用 `display_source` 渲染"📁 groupA (N users) / 👤 ruizhou"的分组视图，
用 `data`（真实 usid）做权限校验与展开明细。二者对不上（组漂移）时以 `data` 为准、给组名标"快照"。



| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/permissions/super-users` | 添加 super_user（operator 需为 super_user） |
| `DELETE` | `/permissions/super-users/{usid}` | 移除 super_user（`operator_usid` 作 query 参数） |
| `GET` | `/permissions/super-users?operator_usid=<super>` | 列出所有 super_user |

```bash
# 列出
curl -H "X-API-Key: $KEY" \
  "http://10.47.202.30:8080/api/v1/permissions/super-users?operator_usid=hanwzhan"
# -> {"success":true,"data":[{"usid":"hanwzhan","note":"bootstrap super_user","created_at":"..."}, ...]}

# 添加
curl -X POST http://10.47.202.30:8080/api/v1/permissions/super-users \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"operator_usid":"hanwzhan","usid":"carol","note":"team lead"}'

# 移除
curl -X DELETE \
  "http://10.47.202.30:8080/api/v1/permissions/super-users/carol?operator_usid=hanwzhan" \
  -H "X-API-Key: $KEY"
```

### 3.3 common_kb —— 公共知识库管理

| 方法 | 路径 | 权限 |
|------|------|------|
| `POST` | `/permissions/common-kbs` | super_user |
| `DELETE` | `/permissions/common-kbs/{kb_id}?operator_usid=<super>` | super_user |
| `GET` | `/permissions/common-kbs` | 任意认证用户 |

```bash
# 标记为公共
curl -X POST http://10.47.202.30:8080/api/v1/permissions/common-kbs \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"operator_usid":"hanwzhan","kb_id":"<KB_ID>","note":"公司公开手册"}'

# 列出（无需 operator_usid）
curl -H "X-API-Key: $KEY" http://10.47.202.30:8080/api/v1/permissions/common-kbs
# -> {"success":true,"data":[{"kb_id":"<KB_ID>","note":"...","created_at":"..."}]}

# 取消公共
curl -X DELETE \
  "http://10.47.202.30:8080/api/v1/permissions/common-kbs/<KB_ID>?operator_usid=hanwzhan" \
  -H "X-API-Key: $KEY"
```

### 3.4 searchable-kbs —— 查询某 usid 可搜索的知识库

```
GET /permissions/searchable-kbs?usid=<usid>
```

返回**该 usid 可搜索的全部知识库**，范围与 MCP `search_chunks` / `chat` 完全一致：
super_user → 全部 KB；否则 → kb_acl 授权（admin+normal 都算）∪ 所有 common_kb。每条带
KB 名称（`name`）、来源（`source`）、以及 acl 授权时的角色（`role`）。

**典型用途**：前端拿这个列表给用户展示"你能搜哪些库"，用户选中某个后，把该 `kb_id`
作为 `search_chunks` / `chat` 的 `kb_ids` 参数传入，即可只搜这一个库。

鉴权：任意认证用户可查询**自己**；查询**别人**的 usid 需 operator 为 super_user
（此时须带 `operator_usid`）。

```bash
# 查自己
curl -H "X-API-Key: $KEY" \
  "http://10.47.202.30:8080/api/v1/permissions/searchable-kbs?usid=alice"

# super_user 代查别人
curl -H "X-API-Key: $KEY" \
  "http://10.47.202.30:8080/api/v1/permissions/searchable-kbs?usid=alice&operator_usid=hanwzhan"
```
返回：
```json
{"success":true,"data":[
  {"kb_id":"968c...","name":"CVDN 测试报告库","source":"acl","role":"admin"},
  {"kb_id":"a1b2...","name":"共享设计文档","source":"acl","role":"normal"},
  {"kb_id":"db83...","name":"China Active Projects","source":"common"}
]}
```

| 字段 | 说明 |
|------|------|
| `kb_id` | 知识库 ID，直接用作 `search_chunks`/`chat` 的 `kb_ids` 元素 |
| `name` | 知识库名称（供展示） |
| `source` | `acl`（个人授权）\| `common`（公共库）\| `super`（该 usid 是 super_user，返回全部库时） |
| `role` | 仅 `source=acl` 时有：`admin` \| `normal` |

> 无可搜库时返回 `{"success":true,"data":[]}`。

### 3.5 REST 接口速查表

| 方法 | 路径 | 操作者要求 | Body / Query |
|------|------|-----------|--------------|
| POST | `/permissions/kb-acl` | KB admin 或 super | body: operator_usid, kb_id, usid, role |
| DELETE | `/permissions/kb-acl` | KB admin 或 super | body: operator_usid, kb_id, usid |
| GET | `/permissions/kb-acl` | 视模式 | query: operator_usid + (kb_id \| usid) |
| **POST** | **`/permissions/kb-acl/batch`** | **空库任意认证（首建）/ 已有名单则 KB admin 或 super** | **body: operator_usid, kb_id, admins[], normals[], display_source?（全量替换）** |
| **GET** | **`/permissions/searchable-kbs`** | **查自己/或 super 查他人** | **query: usid (+ operator_usid)** |
| POST | `/permissions/super-users` | super | body: operator_usid, usid, note |
| DELETE | `/permissions/super-users/{usid}` | super | query: operator_usid |
| GET | `/permissions/super-users` | super | query: operator_usid |
| POST | `/permissions/common-kbs` | super | body: operator_usid, kb_id, note |
| DELETE | `/permissions/common-kbs/{kb_id}` | super | query: operator_usid |
| GET | `/permissions/common-kbs` | 任意认证 | — |

---

## 4. MCP 工具（权限改造）

MCP 端点：`POST /mcp`（HTTP Streamable 协议）。同样需要 `X-API-Key`。

改造后，两个检索类工具 **`search_chunks`** 和 **`chat`** 都新增了必填参数
**`usid`**：系统据此调用 `resolveScope`，只在该 usid 有权访问的 KB 内检索/问答。

### scope 解析规则（两个工具一致）

1. 计算该 usid 的可访问 KB 集合（super_user → 全部；否则 kb_acl ∪ common_kb）。
2. 若调用方额外传了 `kb_ids`，则与上述集合 **取交集**——即 `kb_ids`
   只能 **缩小** 范围，**永远无法越权扩大**范围。
3. 交集为空 → 直接返回空结果（`search_chunks` 返回空列表；`chat` 返回礼貌提示），不报错。

### 4.1 MCP 握手（一次性）

```bash
BASE=http://10.47.202.30:8080
KEY=<your key>

# 1) initialize —— 从响应头拿 Mcp-Session-Id
curl -s -D - "$BASE/mcp" \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"cli","version":"1"}}}'
# 响应头含： Mcp-Session-Id: <SID>

# 2) 发送 initialized 通知（后续所有请求都带 Mcp-Session-Id）
curl -s "$BASE/mcp" \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: <SID>" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
```

### 4.2 search_chunks —— 权限内混合检索

**参数**（注意是 `limit`，不是 `top_k`）：

| 字段 | 类型 | 必填 | 说明 |
|------|------|:----:|------|
| `usid` | string | ✅ | 业务用户 ID，决定检索范围 |
| `query` | string | ✅ | 自然语言查询 |
| `limit` | int | | 全局返回条数上限（1..50，默认 5） |
| `kb_ids` | string[] | | 仅在这些 KB 内检索；与 usid 权限集取交集，不能扩权 |
| `doc_ids` | string[] | | 限定到指定文档 ID |

```bash
curl -s "$BASE/mcp" \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: <SID>" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
        "name":"search_chunks",
        "arguments":{"usid":"hanwzhan","query":"知识库","limit":3}}}'
```

返回（`result.content[0].text` 内是 JSON 字符串）：
```json
{
  "results": [
    {"id":"...","kb_id":"...","content":"...","knowledge_id":"...",
     "knowledge_title":"...","score":0.83,"chunk_index":2}
  ],
  "total": 1
}
```

> 空权限 usid → `{"results":[],"total":0}`（不报错）。
> 若权限内所有 KB 检索都失败，才会返回 `isError` 并汇总各 KB 的错误原因。

### 4.3 chat —— 权限内 RAG 问答

**参数**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|:----:|------|
| `usid` | string | ✅ | 业务用户 ID，决定问答的知识范围 |
| `query` | string | ✅ | 问题 |
| `kb_ids` | string[] | | 限定 KB 子集；与权限集取交集，不能扩权 |
| `session_id` | string | | 多轮会话 ID；省略则新建会话 |

```bash
curl -s "$BASE/mcp" \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: <SID>" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{
        "name":"chat",
        "arguments":{"usid":"hanwzhan","query":"如何创建知识库？"}}}'
```

返回（`result.content[0].text` 内是 JSON 字符串）：
```json
{
  "answer": "……生成的回答……",
  "references": [
    {"knowledge_id":"...","knowledge_title":"...","content":"...","score":0.81}
  ],
  "session_id": "<用于下一轮对话>"
}
```

多轮追问：把上一轮返回的 `session_id` 传回即可。

> 无可访问知识库时，`answer` 为提示语
> "抱歉，你没有可访问的知识库，或指定的知识库不在你的权限范围内。"，`references` 为空。

---

## 5. 典型使用流程

1. **管理员初始化**：用 bootstrap super_user（如 `hanwzhan`）通过 REST 接口：
   - 把公共文档 KB 标记为 `common_kb`（全员可读）；
   - 给业务用户按 KB 授权（`kb-acl`，admin/normal）；
   - 需要时提拔新的 super_user。
2. **业务系统调用**：以真实业务用户的 `usid` 调用 MCP `search_chunks` / `chat`，
   系统自动按该 usid 的权限过滤检索范围，无需业务侧再做过滤。
3. **委派管理**：某 KB 的 `admin` 可自行通过 `kb-acl` 接口增删该 KB 的成员，
   无需每次找 super_user。

---

## 6. 已验证结论（2026-07-07，生产 10.47.202.30）

- ✅ 权限路由已部署并生效（Swagger 静态 spec 未含，属正常，接口实测可用）。
- ✅ migration `000060` 已执行，seed 出 `hanwzhan/whui/ksa` 三个 super_user。
- ✅ `requireSuperUser` / `CanManageKB` / 参数校验均按预期返回 403 / 400。
- ✅ kb_acl 全生命周期、KB admin 自主管理成员、common_kb 增删查全部通过。
- ✅ `search_chunks` 的 `resolveScope` 按 usid 权限差异化裁剪检索范围（super_user 搜全部，受限用户仅搜授权 KB）。
- ⚠️ 与权限无关的既有问题：远程环境检索时报 `repository of type milvus not found`
  （检索引擎注册问题，非本次权限改造引入），需单独排查 milvus 引擎注册 / 环境配置。

## 7. 变更记录（2026-07-08）

本次两处改动（需 `migration 000061` + 重新构建镜像部署）：

1. **batch 首建授权（bootstrap first-grant）**：`POST /permissions/kb-acl/batch` 对**空 roster 的新库**
   放行首次授权——任意认证调用方均可设立初始名单，`operator_usid` 传谁都行；库已有成员后照旧要求 KB
   admin / super_user。解决了"新库没人是 admin、普通用户无法自助建库授权"的先有鸡还是先有蛋死锁。
   （原先只有 super_user 能给全新的库首建授权。）
   - 涉及文件：`internal/handler/permission.go`（`BatchSetKBMembers` 先查 roster 是否为空）。

2. **display_source 组显示（纯展示元数据）**：新增 `migration 000061` 建 `kb_acl_display` 表；
   `POST /permissions/kb-acl/batch` 新增可选字段 `display_source`（原始 group/usid token），
   后端原样存储、`GET /permissions/kb-acl`（模式 A）与 batch 返回体都回带；**绝不参与权限判定**。
   让前端把展开后的一堆 usid 还原成"组"显示。不传该字段则完全等同旧行为。
   - 涉及文件：`migrations/versioned/000061_kb_acl_display.{up,down}.sql`、`internal/types/permission.go`
     （`KBAclDisplaySource` / `KBAclDisplay`）、`internal/types/interfaces/permission.go`、
     `internal/application/repository/permission.go`（`UpsertKBAclDisplay` / `GetKBAclDisplay`）、
     `internal/application/service/permission.go`（`ReplaceKBMembers` 增参 + `GetKBDisplaySource`）、
     `internal/handler/permission.go`。
   - ⚠️ display_source 是**授权时快照**，组成员之后漂移不会自动同步（详见 §3.1）。

