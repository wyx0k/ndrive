# ntd 架构 (v4)

> 高达独角兽 NT-D，无实义。底层 Go 引擎，library-first，被下游系统引用。
> 引擎只负责树的组织、ops 执行、meta 持久化、policy 过滤；所有结构鉴权由外部负责。

---

## 1. 设计原则

1. **引擎不做鉴权**：workspace / policy / bizmodel 的 CRUD 调用一律裸调，外部负责谁有权调
2. **policy 只过滤数据层 op**：bizmodel 上的 read / write / edit 等操作才过 `policy.Check`
3. **依赖单向**：上游依赖下游，下游不反向依赖
4. **强类型 + 显式注册**：所有 driver / config 都是结构体，初始化显式调用无副作用
5. **engine vs driver 分离**：engine 不知道具体 backend 实现，5 个内置 default，外部**不可**自定义/覆盖
6. **单进程优先**：仅 LLM ↔ UI 通信用 bus，不考虑分布式

---

## 2. 系统定位

| ntd 负责 | ntd 不负责 |
|---|---|
| 树形组织 + ops 执行 | 用户认证 / 身份管理 |
| 5 种 storage backend 抽象 + 5 个 default 实现 | 业务模型 (Note/Task/...) 定义 |
| meta 持久化（sqlite / mysql） | UI / Agent / Memory 实现 |
| snowflake 节点 ID | multi-instance 协调 |
| policy 执行（仅作用于 bizmodel data ops） | 谁能调 workspace/policy/bizmodel 的 CRUD |
| owner_id 作为 ctx 透传字段 | owner 身份语义本身 |

**外部系统负责**：定义业务模型、提供 owner_id、选 driver、注册 backend、决定谁能调元数据 CRUD。

> **约定**：`owner_id` 的语义（userID / agentID / 其他）由外部系统保证全局唯一且语义一致，引擎不做任何校验。`bus` 是单进程通道，跨进程部署时冲突检测与 policy 询问均不自动 scale，需下游自行替换 bus 实现。

---

## 3. 分层架构

| 层 | 名称 | 包 | 引擎内 check policy? |
|---|---|---|:-:|
| L5 | 外部系统 | - | - |
| L4 | 智能体 | `agent / llm / mem / tools` [占位 → fantasy] | - |
| L3 | 表现 | `service / ui` [占位] | - |
| L2 | 控制 | `policy` + `bus` | - |
| L1 | 工作区 | `workspace` + `bizmodel` + `subkind` + `tree` | ✅ 仅 bizmodel data ops |
| L0 | 基础 | `meta` + `id` | - |

**依赖方向**：严格自上而下。

### 鉴权边界表

| 操作 | 引擎内 `policy.Check`? | 理由 |
|---|:-:|---|
| `workspace.Create / Delete` | ❌ | 结构操作 |
| `workspace.CreateBizmodel / DeleteBizmodel` | ❌ | 结构操作 |
| `policy.Set / Get / List / Delete` | ❌ | policy 本身是配置 |
| `meta.Put / Get / Delete / List` | ❌ | 基础设施 |
| `bus.Publish / Subscribe / Request` | ❌ | 基础设施 |
| `id.Init / Next` | ❌ | 基础设施 |
| **`bizmodel.Call` (任意 op)** | ✅ | 写 op 是 LLM 主要约束对象 |
| **`bizmodel.Tree().Get / List / ListWithBrief / Delete`** | ✅ | 同样受 policy 约束 |

> 引擎不鉴权 → 外部包一层 middleware/handler 做 owner_id 校验即可。

---

## 4. 模块结构

```
ntd/
├── pkg/
│   ├── ntd/                  # ★ 总入口：OpenWorkspace
│   ├── meta/                  # GORM，各 kind 一张表
│   │   ├── meta.go            # Store interface
│   │   ├── sqlite/            # sqlite driver
│   │   └── mysql/             # mysql driver
│   ├── id/                    # snowflake int64，worker_id 默认 1
│   ├── tree/                  # Tree 接口 + Node / Brief
│   ├── subkind/               # backend 自定义存储类型
│   ├── bus/                   # per-workspace pub/sub + req/resp
│   ├── workspace/             # workspace 句柄 + meta-ops
│   │   ├── workspace.go
│   │   ├── ctx/               # owner_id / workspace_id ctx keys
│   │   ├── policy/            # (user, ws) → r/w-all/w-ask + Check 接口
│   │   └── bizmodel/
│   │           ├── bizmodel.go
│   │           ├── modeltype.go
│   │           ├── schema.go
│   │           └── backend/   # 5 个 default 实现
│   │               ├── backend.go
│   │               ├── localfile/
│   │               ├── localsqlite/
│   │               ├── localbadger/
│   │               ├── remotesql/
│   │               └── remotemongo/
│   ├── service/               # [占位]
│   ├── mem/                   # [占位]
│   ├── ui/                    # [占位]
│   ├── agent/                 # [占位]
│   ├── llm/                   # [占位]
│   └── tools/                 # [占位]
├── cmd/
│   └── ntd/
├── examples/
│   └── basic/
├── ARCHITECTURE.md
├── README.md
├── go.mod
└── go.sum
```

---

## 5. 核心抽象

### 5.1 Node（树节点）

```go
type Node struct {
    ID       int64          // snowflake
    ParentID int64
    Path     string         // URI-like, slug 分段
    SubKind  string         // 存储级类型，backend 设（"file" / "dir"）
    BizModel string         // 业务级类型，下游设（"Note"）
    Meta     map[string]any
    Data     []byte
}
```

### 5.2 Brief（LLM 提示摘要）

```go
type Brief struct {
    ID       int64
    Path     string
    SubKind  string
    BizModel string
    Title    string         // 可选
    Preview  string         // backend 提取的摘要（前 100 字符 / frontmatter excerpt / 后端存的 hint）
}
```

`Brief` **不**包含 `ParentID` / `Meta` / `Data`，专为 LLM prompt 上下文设计。

`Preview` 格式约定：纯文本，不含 markdown 语法，最大 200 字节，超出截断，由 backend 负责 normalize。

### 5.3 Tree（树接口）

```go
type Tree interface {
    Get(path string) (*Node, error)
    List(prefix string) ([]*Node, error)              // 全数据
    ListWithBrief(prefix string) ([]*Brief, error)    // 仅摘要（LLM 用）
    Delete(path string) error
}
```

> Tree 是**读/删视图**，节点创建不经过此接口，由各 backend 的 op 直接写存储（如 `file-fm-write`）。接入方不应在 Tree 上寻找 Create 入口。Tree 的并发安全由 backend 实现保证，引擎层不加额外锁。

### 5.4 BizModelType（全局目录）

```go
type BizModelType struct {
    Name        string              // "local_file"
    Description string              // "本地文件系统，路径即节点"
    SubKinds    []subkind.Kind      // 该 backend 支持的 subKind
    Ops         []tree.NamedOp      // 聚合所有 SubKind 的 ops
}
```

5 个 default backend 启动时自动注册到 `backend` 内部 catalog，`bizmodel/types` 对外暴露只读视图。**下游不可注册新 backend**。

### 5.5 Backend

```go
type Backend interface {
    Name() string
    SubKinds() []subkind.Kind
    OpsForSubKind(kind string) []tree.NamedOp
    NewTree(cfg Config) (tree.Tree, error)
}
type Config interface{ isBackendConfig() }
```

包内 `Register / Get / Types` 仅引擎 init 阶段使用，下游不可访问。

### 5.6 SubKind

```go
type Kind struct {
    Name        string
    Description string
    Schema      Schema
    Ops         []string
}
type Schema struct {
    Required []Field
    Optional []Field
}
type Field struct { Name, Type string }
```

### 5.7 Policy Mode

```go
type Mode int
const (
    RAsk Mode = iota // 读也询问（最低权限）
    R                // 只读，写全拒
    WAsk             // 写时询问，读直通
    W                // 读写全直通（最高权限）
)

type Binding struct {
    WorkspaceID string
    OwnerID     string
    Mode        Mode
}
```

权限递增：`RAsk < R < WAsk < W`。

### 5.8 Op（后端操作）

```go
type Op func(ctx context.Context, t Tree, args OpArgs) (any, error)
type OpArgs struct {
    Path string
    Args any
}
type NamedOp struct {
    Name        string         // "file-fm-write"
    Description string
    Schema      map[string]any // JSON Schema，引擎层校验 args
    Op          Op
}
```

**op 命名约定**：`"<subkind>-<op>"`，例 `file-fm-write` / `dir-mkdir`。跨 SubKind 不重名，命名空间天然分离。

---

## 6. 关键决策表（27 条）

| # | 项 | 决定 |
|---|---|---|
| 1 | 引擎定位 | library-first，无 user，只有 owner_id（外部透明串）|
| 2 | 树 | 仅作 biz-model 的组织形式，无他用 |
| 3 | bizmodel_type | 5 种（= 5 个 default backend），root 隔离 |
| 4 | 实例化 | 每 type 可多棵 |
| 5 | 路径 | URI-like，slug 分段 |
| 6 | 节点字段 | ID, ParentID, Path, BizModel, SubKind, Meta, Data |
| 7 | BizModel | Node 字段，下游设（业务级）|
| 8 | SubKind | backend 自定义（存储级，dir/file 等）|
| 9 | backend 接口 | SubKinds() + OpsForSubKind() + NewTree() |
| 10 | backend 数量 | **封闭**：5 个 default，下游不可自定义 |
| 11 | backend 注册 | 手动（ntd init 阶段一次性注册 5 个）|
| 12 | backend 重名 | 报错 |
| 13 | meta | GORM，各 kind 一张表；全部 kind 见下表 |
| 13a | meta kind 目录 | `workspace` / `bizmodel` / `policy_binding` |
| 14 | meta 驱动 | sqlite / mysql 二选一，default driver 都内置 |
| 15 | meta 注册 | 显式 `New(ctx, cfg)`，无 init 副作用 |
| 16 | meta 配置 | 强类型 struct（SqliteConfig / MysqlConfig）|
| 17 | meta schema | AutoMigrate 自管 |
| 18 | ID | bwmarrin/snowflake，int64，worker_id 默认 1 |
| 19 | policy | (user, ws) → RAsk/R/WAsk/W 四档，主要约束 LLM |
| 20 | policy 触发 | 仅 bizmodel data ops 时 Check |
| 21 | 引擎不鉴权 | 结构 CRUD 全部裸调 |
| 22 | policy 询问 | per-workspace bus；无订阅者 → 静默 deny |
| 23 | policy 持久化 | **持久化到 meta**，kind=`policy_binding`，随 GORM 管理，重启后恢复 |
| 24 | bus 范围 | 单进程 llm↔ui 通信；跨进程部署需下游自行替换 bus 实现 |
| 25 | Tree 缓存 | **不缓存**（LLM 实时读，UI 自行 watch + 更新已加载数据）|
| 26 | 冲突检测 | llm+ui 同时写 → error + bus 通知监听者 |
| 27 | 依赖方向 | 单向，下游不依赖上游 |

---

## 7. 数据流

### 7.1 写入（唯一触发 policy.Check 的路径）

```
外部 op (ctx with wsID, owner_id)
    ↓
bizmodel.Call(ctx, opName, path, args)
    ↓
1. policy.Check(ctx, opName)
   ├─ RAsk   → bus.Request("policy.ask", payload)（读也询问）
   ├─ R      → 直通（只读，write op 在此被拒）
   ├─ WAsk   → bus.Request("policy.ask", payload)
   │              ├─ 无订阅者 → 静默 deny（返 ErrPolicyDenied）
   │              └─ 有订阅者 → 等回复 → 通过/拒绝
   └─ W      → 直通
2. 通过 path 查节点，确定其 SubKind（path 不存在则报错）
3. 校验 opName 属于该 SubKind，不合法则拒绝
4. JSON Schema 校验 args
5. op.Op(ctx, tree, OpArgs{Path, Args})
6. 返回结果
```

### 7.2 读取

```
bizmodel.Tree().ListWithBrief(prefix)
    → policy.Check(ctx, op="read")
      ├─ RAsk → bus.Request 询问
      ├─ R / WAsk / W → 直通
    → backend.Tree.ListWithBrief
    → Store.List
    → 返回 []*Brief
```

### 7.3 结构 CRUD（外部鉴权）

```
外部 (已鉴权)
    → workspace.Create / Delete / CreateBizmodel / ...
    → meta.Put(kind="workspace", ...)
```

### 7.4 元数据

```
任何结构 CRUD
    → meta.Store.Put/Get/Delete/List
    → sqlite / mysql GORM 表
```

---

## 8. ★ Use Case 1：创建带 frontmatter 的 markdown 笔记

### 流程图

```
┌─────────────────────────────────────────────────────────────────┐
│ LLM Agent（charm.land/fantasy）                                │
└────────────────────────┬────────────────────────────────────────┘
                         │
   Step 2: 探索可用 bizmodel
                         │
   ws.ListBizModels(ctx)
                         ▼
┌────────────────────────────────────────────────┐
│ Workspace（pkg/workspace）                      │
│ · meta 读所有 bizmodel 记录                     │
│ · 关联 BizModelType 目录                         │
│ · 返回 []*Bizmodel（每个带 Type）               │
└────────────────────────┬───────────────────────┘
                         │
   Step 3: 拿到 note bizmodel + ops
                         ▼
┌────────────────────────────────────────────────┐
│ Bizmodel                                       │
│ · bm.Type() → SubKinds={dir,file}              │
│ · bm.Ops()   → [file-write, file-fm-write, ...]│
└────────────────────────┬───────────────────────┘
                         │
   Step 4: 看现有结构
                         │
   bm.Tree().ListWithBrief("/notes")
                         ▼
┌────────────────────────────────────────────────┐
│ Tree（localfile backend 实现）                  │
│ · ListWithBrief → Store 读元数据                │
│ · 返回 []*Brief{SubKind, BizModel, Preview}     │
└────────────────────────┬───────────────────────┘
                         │
   LLM 拿到 Brief[]，识别 SubKind="dir" 的节点
                         │
   Step 5: 选定目标路径
                         │
   bm.Call(ctx, "file-fm-write", "/notes/travel/japan.md", args{...})
                         ▼
┌────────────────────────────────────────────────┐
│ Bizmodel.Call()                                 │
│ · 1. policy.Check(ctx, "file-fm-write")         │
│ │    └─ R/W 直通；RAsk/WAsk → bus.Request 询问 │
│ · 2. 通过 path 确定 SubKind，校验 op 合法性     │
│ · 3. JSON Schema 校验 args                      │
│ · 4. op.Op(ctx, tree, OpArgs{Path, Args})       │
└────────────────────────┬───────────────────────┘
                         ▼
┌────────────────────────────────────────────────┐
│ Backend localfile.fm-write op                  │
│ · 1. Store.WriteFile(/notes/travel/japan.md)   │
│ · 2. 序列化 frontmatter 为 YAML                 │
│ · 3. 写入文件                                   │
└────────────────────────────────────────────────┘
```

### API 调用链（精确到行）

```go
// 1. LLM 拿到 workspace 句柄
ws, _ := ntd.OpenWorkspace(ctx, wsID, ownerID, cfg)

// 2. 列所有 bizmodel
bizmodels, _ := ws.ListBizModels(ctx)

// 3. 找到 note 业务的 bizmodel
var noteBiz *bizmodel.Bizmodel
for _, b := range bizmodels {
    if hasFileSubKind(b.Type().SubKinds) {
        noteBiz = b
        break
    }
}

// 4. 看树结构
briefs, _ := noteBiz.Tree().ListWithBrief(ctx, "/notes")

// 5. 找到 SubKind="dir" 的位置
var parentDir string
for _, br := range briefs {
    if br.SubKind == "dir" {
        parentDir = br.Path
        break
    }
}

// 6. 调后端 op 创建 md 文件
newPath := parentDir + "/japan.md"
noteBiz.Call(ctx, "file-fm-write", newPath, map[string]any{
    "withfm": true,
    "fm":     map[string]any{"date": "2024-01-15", "tags": []string{"travel"}},
    "body":   "...",
})
```

---

## 9. ★ Use Case 2：policy WAsk 询问流程

```
LLM 调 bm.Call(ctx, "file-fm-write", "/notes/foo.md", args{...})
   │
   ▼
Bizmodel.Call 入口
   │
   ├─ mode = RAsk   → 走询问流程（读也问）
   ├─ mode = R      → 直通（write op 被拒）
   ├─ mode = WAsk   → 走询问流程
   └─ mode = W      → 直通
                        │
                        ▼
   policy.NewChecker(store, wsID, ownerID, bus)
       .Check(ctx, "file-fm-write")
                        │
                        ▼
   bus.Request(ctx, "policy.ask", AskPayload{
       WorkspaceID: wsID,
       OwnerID:     ownerID,
       Op:          "file-fm-write",
       Path:        "/notes/foo.md",
       Preview:     "...",  // ★ op 提供的人类可读预览
   })
                        │
                        ▼
   ┌──────────────────────────────────────────┐
   │ 订阅分支                  │
   │ UI 侧订阅 bus.Subscribe("policy.ask", ...)│
   │  → 弹出确认窗口                          │
   │  → 用户点 "Yes" / "No"                   │
   │  → 回 Response{Approved: bool}           │
   └────────────────────┬─────────────────────┘
                        │
   ┌────────────────────┴─────────────────────┐
   │ 无订阅者分支                              │
   │ bus.Request 立即返 ErrNoSubscriber       │
   │ → 静默 deny → 返 ErrPolicyDenied        │
   └──────────────────────────────────────────┘
                        │
                        ▼
   bus.Request 返回 Response
   ├─ Approved = true  → 继续 op.Op(...)
   └─ Approved = false → 返 ErrPolicyDenied
```

> **WAsk in-flight 请求不持久化**：进程重启时中途的 ask 丢失（可接受）。Binding 本身持久化到 meta，重启后恢复。

---

## 10. 关键设计模式

| 模式 | 用在哪 | 目的 |
|---|---|---|
| **单例 Workspace** | workspace.OpenWorkspace | 同一 wsID 多次调用返同一句柄，bus/policy/缓存共享 |
| **create-or-open** | OpenWorkspace | 不存在则创建，存在且 ownerID 一致则打开 |
| **ownerID 一致性保护** | OpenWorkspace | 防御性检查，ownerID 不符即报错（非鉴权）|
| **封闭 Backend** | 5 个 default 注册 | 下游不可注册/覆盖，避免 API 表面爆炸 |
| **JSON Schema 校验** | Bizmodel.Call 入口 | args 校验统一在引擎层，op 内部不重复 |
| **op 命名空间** | "subkind-op" | 天然避免重名，LLM 清晰理解 |
| **Per-workspace 隔离** | bus / policy / tree | workspace 是隔离单元 |
| **typed errors** | 包级 ErrXxx | 调用方方便用 errors.Is 判别 |
| **GORM 事务** | CreateBizmodel | meta 写入和 backend.NewTree 一起回滚 |
| **强类型 Config** | SqliteConfig / MysqlConfig | 无 DSN 字符串解析坑 |

---

## 11. Workspace 生命周期详细

### OpenWorkspace 行为表

| 情况 | 行为 |
|---|---|
| wsID 不存在 | 用传入的 ownerID 创建新 workspace |
| wsID 存在 + ownerID 一致 | 打开现有 workspace（幂等）|
| wsID 存在 + ownerID 不一致 | 报错 `ErrOwnerMismatch` |

### OpenWorkspace 伪代码

```go
func OpenWorkspace(ctx context.Context, wsID, ownerID string, cfg Config) (*workspace.Workspace, error) {
    existing, _ := metaStore.Get(ctx, wsID)
    switch {
    case existing == nil:
        ws := workspace.New(wsID, ownerID, ...)
        metaStore.Put(ctx, wsID, serialize(ws))
        return ws, nil
    case existing.OwnerID == ownerID:
        ws := workspace.FromMeta(existing)
        return ws, nil
    default:
        return nil, fmt.Errorf("%w: ws=%s, want=%s, got=%s",
            ErrOwnerMismatch, wsID, existing.OwnerID, ownerID)
    }
}
```

---

## 12. 落地顺序

1. `pkg/id` （10 行，立刻可跑）
2. `pkg/meta` （sqlite + mysql，GORM AutoMigrate）
3. `pkg/tree` （Node + Brief + Tree 接口）
4. `pkg/subkind` （SubKind 类型）
5. `pkg/bizmodel/backend` （接口 + 5 个 default 实现）
6. `pkg/bizmodel` （Registry 串联）
7. `pkg/policy` + `pkg/bus`
8. `pkg/workspace`（meta-ops + ctx）
9. `pkg/ntd`（OpenWorkspace 总入口）
10. 占位包 README
11. `cmd/ntd` + `examples/basic`