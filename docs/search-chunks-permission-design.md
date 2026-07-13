# WeKnora search_chunks 权限管理设计（方案 A：独立权限表）

> 状态：**设计稿，未实现**。本文只定义数据模型、流程与接口，不含代码改动。
> 目标：把 WeKnora 的 MCP `search_chunks` 工具作为知识检索能力开放给多个**可信内部项目**，并按 `usid` 做知识库级权限控制。

---

## 0. 背景与前提（务必先读）

### 0.1 架构选型
- 调用方是**纯 MCP 客户端**：输入自然语言，直连 WeKnora 的 `/mcp` 端点。
- 调用方只能提供 `usid`（业务用户 ID），中间**没有独立应用层**做权限过滤。
- 结论：权限逻辑**只能落在 WeKnora 内部**（本方案）。

### 0.2 认证事实（来自 `internal/middleware/auth.go:188-268`）
- 一个 `X-API-Key`（`sk-1IRF...`）= **一个 tenant**，不是一个用户。
- API Key 进来后，WeKnora 固定解析为：`tenant_id=10000` + 合成用户 `system-10000` + `TenantRoleAdmin`。
- WeKnora 自带的 RBAC（Owner/Admin/Contributor/Viewer）是给 **JWT 真人登录**用的，**API Key 通道绕过它**。

### 0.3 usid 的信任模型（安全边界，必须接受）
- `usid` 作为 `search_chunks` 的**工具入参**传入，属于**调用方自证**。
- WeKnora **无法验证**"调用方真的是这个 usid"——所有调用方共用同一个 API Key。
- **本方案的权限拦截的是"诚实但无权"的访问，拦不住"持有 Key 故意伪造 usid"的攻击。**
- 因当前调用方均为**可信内部系统**，此前提可接受。若未来引入不可信调用方，需改为"每 usid 独立发 token"的强认证方案（不在本设计范围）。

---

## 1. 数据模型（3 张新表）

三张表都是"贴在 WeKnora 旁边"的**权限层**，不修改 `knowledge_bases` 等核心表。存 WeKnora 自身的 PostgreSQL。

### 1.1 `kb_acl` — 知识库 ↔ 用户 ↔ 角色

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `id` | bigserial | PK | 自增主键 |
| `kb_id` | varchar(36) | NOT NULL | WeKnora 知识库 ID（对应 `knowledge_bases.id`） |
| `usid` | varchar(64) | NOT NULL | 业务用户 ID（调用方的用户标识） |
| `role` | varchar(16) | NOT NULL | `admin` \| `normal` |
| `created_at` | timestamptz | NOT NULL DEFAULT now() | |
| `updated_at` | timestamptz | NOT NULL DEFAULT now() | |

**唯一约束**：`UNIQUE(kb_id, usid)` —— 一个用户在一个库里只有一个角色。
**索引**：`INDEX(usid)`（按用户查他能进哪些库，检索路径高频用到）；`INDEX(kb_id)`（按库列成员）。

DDL：

```sql
CREATE TABLE kb_acl (
    id         BIGSERIAL PRIMARY KEY,
    kb_id      VARCHAR(36) NOT NULL,
    usid       VARCHAR(64) NOT NULL,
    role       VARCHAR(16) NOT NULL CHECK (role IN ('admin', 'normal')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_kb_acl_kb_usid UNIQUE (kb_id, usid)
);
CREATE INDEX idx_kb_acl_usid  ON kb_acl (usid);
CREATE INDEX idx_kb_acl_kb_id ON kb_acl (kb_id);
```

### 1.2 `super_user` — 全库权限用户

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `usid` | varchar(64) | PK | 超级用户 ID，拥有**所有**知识库权限 |
| `note` | varchar(255) | | 备注（可选，如"平台管理员"） |
| `created_at` | timestamptz | NOT NULL DEFAULT now() | |

DDL：

```sql
CREATE TABLE super_user (
    usid       VARCHAR(64) PRIMARY KEY,
    note       VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 1.3 `common_kb` — 公共知识库（无视权限，人人可查）

| 字段 | 类型 | 约束 | 说明 |
|---|---|---|---|
| `kb_id` | varchar(36) | PK | 公共库 ID，任何 usid 都能查 |
| `note` | varchar(255) | | 备注（如"公司制度库"） |
| `created_at` | timestamptz | NOT NULL DEFAULT now() | |

DDL：

```sql
CREATE TABLE common_kb (
    kb_id      VARCHAR(36) PRIMARY KEY,
    note       VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

> **注意**：三张表都不带 `tenant_id`。因为整个系统跑在单一 API Key（单 tenant）下，权限维度是 `usid`，不是 tenant。若未来要多 tenant，再补 `tenant_id` 列 + 复合唯一约束。

---

## 2. 权限判定：usid 能查哪些知识库

### 2.1 核心公式

```
allowed_kbs(usid) =
    IF usid ∈ super_user:
        <所有知识库 ID>            -- 调 kbService.ListKnowledgeBases 拿全量
    ELSE:
        { kb_id | (kb_id, usid) ∈ kb_acl }   -- admin 和 normal 都算“有权限”

search_scope(usid) = allowed_kbs(usid) ∪ { kb_id | kb_id ∈ common_kb }
```

- **查询权限**：只要 usid 在某库的 `kb_acl` 里有记录（无论 admin 还是 normal）→ 可查该库。满足需求"只要在 admin 或 normal 任一即可查"。
- **公共库**：`common_kb` 里的库无条件并入，与 usid 有无权限无关。
- **super_user**：直接 = 全部库（common_kb 是其子集，自动包含，无需特判）。
- **并集去重**：某库既在 acl 又在 common_kb 时，取并集自然去重，结果一致。

### 2.2 admin 与 normal 的区别

| 能力 | normal | admin | super_user |
|---|---|---|---|
| **查询该库**（search_chunks） | ✅ | ✅ | ✅（所有库） |
| **管理该库成员**（增删 kb_acl 记录） | ❌ | ✅（仅该库） | ✅（所有库） |
| **管理 super_user / common_kb** | ❌ | ❌ | ✅ |

> 即：在"检索"这件事上 admin 与 normal 无差别；区别只在"能否管理该库的用户名单"。（本设计确认按此实现。）

---

## 3. search_chunks 工具改造

### 3.1 入参变化

现状（`internal/mcpserver/tools.go:275-280`）：

```go
type searchChunksInput struct {
    KBID   string   `json:"kb_id"`
    Query  string   `json:"query"`
    Limit  int      `json:"limit,omitempty"`
    DocIDs []string `json:"doc_ids,omitempty"`
}
```

改造后（新增 `usid`，`kb_id` 变为可选覆盖）：

| 参数 | 必需 | 说明 |
|---|---|---|
| `usid` | ✅ | 业务用户 ID。**决定检索范围**。 |
| `query` | ✅ | 自然语言检索词 |
| `limit` | 可选 | 全局 top-K，默认 5，范围 1–50 |
| `kb_ids` | 可选 | 显式指定要查的库子集；**必须是 usid 有权限的库 ∩ 此列表**（不能越权）。不传=查 usid 全部可见库 |
| `doc_ids` | 可选 | 限定文档范围（沿用现有语义） |

> 设计取舍：`kb_ids` 传入时做**交集**（`search_scope(usid) ∩ kb_ids`），保证调用方即便乱传库 ID 也不会越权。不传则查 usid 的全部 `search_scope`。

### 3.2 检索流程（伪代码）

```
function search_chunks(usid, query, limit=5, kb_ids=[], doc_ids=[]):
    require usid, query 非空

    # 1. 求 usid 的检索范围
    if usid in super_user:
        scope = all_kb_ids()                       # kbService.ListKnowledgeBases
    else:
        scope = select kb_id from kb_acl where usid = ?
    scope = scope ∪ (select kb_id from common_kb)   # 并入公共库，去重

    # 2. 若调用方指定了 kb_ids，取交集防越权
    if kb_ids not empty:
        scope = scope ∩ kb_ids

    if scope is empty:
        return { results: [], total: 0 }            # 无权限或无库，返回空

    # 3. 对 scope 里每个库做单库混合检索（WeKnora 原生 HybridSearch，并发）
    all_hits = []
    parallel for kb_id in scope:
        hits = kbService.HybridSearch(ctx, kb_id, { QueryText: query,
                                                    MatchCount: limit,
                                                    KnowledgeIDs: doc_ids })
        all_hits.append(hits)   # 每条 hit 附带来源 kb_id

    # 4. 全局按 score 降序合并，取 top-limit
    merged = sort(all_hits, by=score desc)[:limit]

    return { results: merged, total: len(merged) }
```

### 3.3 关键实现注意点

1. **多库检索是循环调用单库 `HybridSearch`**：WeKnora 的 `HybridSearch(ctx, kb_id, params)` 是单库接口。多库时并发调用后在应用层合并。检索无 LLM，成本低、可并发。
2. **每个 hit 要带来源库**：返回结构里在现有 `chunkResult` 上增加 `kb_id` 字段，让调用方知道 chunk 来自哪个库。
3. **top-K 语义**：`limit` 是**全局** top-K，不是每库 K。先每库取 `limit` 条候选，再全局排序截断（保证召回足够再截断）。
4. **score 可比性**：不同库若用不同 embedding 模型，score 尺度可能不一致，全局排序可能有偏差。**已知风险**，本设计先按原始 score 合并；若实际效果差，后续可加"分库归一化"或"每库配额"。
5. **ctx 里的 tenant_id 不变**：仍走原有 `enrichCtx` 注入的 tenant，`HybridSearch` 的 tenant 隔离照常生效——权限层是叠加在 tenant 隔离之上的额外约束。

---

## 4. 权限管理接口

对三张表做 CRUD。可实现为 **MCP 工具**（调用方用 MCP 统一）或 **REST 接口**（走 `/api/v1/...`）。建议 **REST**，因为管理操作通常由后台/脚本执行，且便于加审计。以下按 REST 描述。

### 4.1 kb_acl 管理（库成员）

| 方法 | 路径 | 说明 | 谁能调 |
|---|---|---|---|
| POST | `/api/v1/kb-acl` | 给某库加用户 `{kb_id, usid, role}` | 该库 admin 或 super_user |
| DELETE | `/api/v1/kb-acl` | 移除用户 `{kb_id, usid}` | 该库 admin 或 super_user |
| GET | `/api/v1/kb-acl?kb_id=X` | 列该库所有成员 | 该库 admin 或 super_user |
| GET | `/api/v1/kb-acl?usid=Y` | 列某用户能进的所有库 | super_user（或本人查自己） |

### 4.2 super_user 管理

| 方法 | 路径 | 说明 | 谁能调 |
|---|---|---|---|
| POST | `/api/v1/super-users` | 添加超级用户 `{usid}` | super_user |
| DELETE | `/api/v1/super-users/{usid}` | 移除超级用户 | super_user |
| GET | `/api/v1/super-users` | 列出所有超级用户 | super_user |

### 4.3 common_kb 管理（公共库）

| 方法 | 路径 | 说明 | 谁能调 |
|---|---|---|---|
| POST | `/api/v1/common-kbs` | 设某库为公共 `{kb_id, note}` | super_user |
| DELETE | `/api/v1/common-kbs/{kb_id}` | 取消公共 | super_user |
| GET | `/api/v1/common-kbs` | 列出所有公共库 | 任何已认证调用方 |

### 4.4 "谁能调"的判定（管理操作的权限入口）

管理接口同样以 `usid` 自证判定（可信内部前提下）：
- **super_user 操作**：请求带 `operator_usid`，校验 `operator_usid ∈ super_user`。
- **库 admin 操作**：校验 `(kb_id, operator_usid, role='admin') ∈ kb_acl` 或 `operator_usid ∈ super_user`。

> **引导问题**：初始时没有任何 super_user，谁来加第一个？
> 解决：**第一个 super_user 通过 migration 或部署时的 seed 脚本直接写库**（比如平台管理员的 usid），或提供一个仅本机/仅 Owner-JWT 可调的 bootstrap 接口。之后由 super_user 自行管理。

---

## 5. 实现改动清单（供后续开发参考，本文不实现）

| 改动项 | 位置 | 内容 |
|---|---|---|
| 建表 migration | `migrations/versioned/` | 新增 3 张表的 up/down SQL |
| 权限层 model | `internal/types/` | `KBAcl` / `SuperUser` / `CommonKB` 结构体 |
| 权限 repository | `internal/repository/` | 三张表的 CRUD + `resolveScope(usid)` 查询 |
| 权限 service | `internal/application/service/` | `PermissionService`：`GetSearchScope(usid)`、成员管理、super/common 管理 |
| 改 search_chunks 工具 | `internal/mcpserver/tools.go` | 入参加 `usid`/`kb_ids`；改为多库检索 + 合并；结果加 `kb_id` |
| 管理 REST 路由 | `internal/handler/` + `internal/router/router.go` | 4.1–4.3 的接口 + handler |
| bootstrap 首个 super_user | migration seed 或 Owner-only 接口 | 见 4.4 |

---

## 6. 边界与已知限制（写进文档备查）

1. **usid 可伪造**：可信内部前提下接受；不可信场景需改强认证（每 usid 独立 token）。
2. **跨库 score 不可比**：不同 embedding 模型的库合并排序可能有偏差；先接受，必要时加归一化/配额。
3. **多库检索是 N 次单库调用**：并发执行，检索无 LLM 成本可控；但库极多（几十上百）时延迟会上升，可加"最大并发库数"上限。
4. **只改了 search_chunks**：`chat`（生成式问答）工具**未纳入**本设计。如需 chat 也按 usid 权限跨库，需同样改造 `chat` 工具（复用 `GetSearchScope`）。
5. **公共库噪声**：公共库对所有人可见，若内容与专用库主题差异大，可能引入检索噪声——由公共库内容治理承担，不在权限层解决。

---

## 7. 待确认项（开发前需拍板）

- [ ] 管理接口用 **REST**（本文默认）还是 **MCP 工具**？
- [ ] 首个 super_user 的引导方式：migration seed / Owner-JWT bootstrap 接口 / 其他？
- [ ] `chat` 工具是否也要纳入同一套权限（本设计仅覆盖 search_chunks）？
- [ ] 跨库 score 归一化：先不做（本文默认）还是首版就加"每库配额"？
