# ntd

名称灵感来自《高达 UC》中的 NT-D（NewType Destroyer）系统——一套在特定条件下被激活、以极高精度响应意图的驱动系统。ntd 取其意：在权限触发时精准响应，把业务意图直接驱动到存储层。

树形业务模型存储引擎。把任意业务数据组织成路径树，统一读写、搜索和权限控制。

## 核心概念

**Workspace** — 一个独立的工作空间，绑定一个 owner，持有 meta 存储和事件总线。

**Bizmodel** — 业务模型实例，对应一棵树和一个存储 backend。同一个 workspace 可以挂多个 bizmodel。

**Tree** — 路径树视图，支持按路径读取、列举、模糊搜索、删除。节点的创建通过 op 完成。

**Op** — backend 提供的操作，命名约定 `<subkind>-<动作>`，例如 `dir-mkdir`、`file-fm-write`。

**Policy** — 四档权限：`RAsk < R < WAsk < W`，持久化到 meta，通过 `ws.SetPolicy` 设置。`WAsk/RAsk` 模式下写/读操作会通过事件总线向外请求确认。

**Bus** — 进程内发布/订阅 + 同步请求通道，供下游 LLM pipeline 和 UI 层挂载。

## 快速上手

```go
import "ntd"

ctx := context.Background()

// 打开 workspace，SQLite 作为 meta 存储
ws, err := ntd.OpenWorkspace(ctx, "ws-1", "owner-alice", ntd.Config{
    Meta: ntd.SqliteConfig{Path: "./meta.db"},
})
defer ws.Close()

// 创建 bizmodel，使用本地文件 backend
bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
    ID:       "notes",
    TypeName: ntd.BackendLocalFile,
    Config:   ntd.LocalFileConfig{RootDir: "./notes"},
})

// 设置权限为完全写入
ws.SetPolicy(ctx, ntd.W)

// 注入 wsID/ownerID 到 ctx
ownerCtx := ws.Context(ctx)

// 执行 op
bm.Call(ownerCtx, "dir-mkdir", "/2024", nil)
bm.Call(ownerCtx, "file-fm-write", "/2024/note.md", map[string]any{
    "withfm": true,
    "fm":     map[string]any{"title": "My Note"},
    "body":   "# My Note\n\nHello.",
})

// 读树
briefs, _ := bm.Tree().ListWithBrief(ownerCtx, "/2024")
results, _ := bm.Tree().Search(ownerCtx, "*note*")
```

## 内置 Backend

| 常量 | 存储 |
|---|---|
| `BackendLocalFile` | 本地文件系统 |
| `BackendLocalSQLite` | 本地 SQLite |
| `BackendLocalBadger` | 本地 BadgerDB |
| `BackendRemoteSQL` | 远端 SQL（MySQL 等） |
| `BackendRemoteMongo` | 远端 MongoDB |

## 扩展 Backend

实现 `ntd.BackendProvider` 接口并注册，无需导入任何内部包：

```go
type MyConfig struct{ Dir string }
func (MyConfig) IsBackendConfig() {}

type MyBackend struct{}
func (MyBackend) Name() string                                          { return "my_backend" }
func (MyBackend) SubKinds() []ntd.SubKind                              { ... }
func (MyBackend) OpsForSubKind(kind string) []ntd.ExternalOp           { ... }
func (MyBackend) NewTree(cfg ntd.BackendConfig) (ntd.Tree, error)      { ... }
func (MyBackend) ConfigFromJSON(data []byte) (ntd.BackendConfig, error) { ... }

func init() {
    ntd.RegisterBackend(MyBackend{})
}
```

## Meta 存储

| 类型 | 说明 |
|---|---|
| `SqliteConfig` | 本地 SQLite，适合单机 |
| `MysqlConfig` | 远端 MySQL，适合多实例 |

## Policy

```go
ws.SetPolicy(ctx, ntd.W)     // 完全写入
ws.SetPolicy(ctx, ntd.WAsk)  // 写操作需确认（通过 bus 向外请求）
ws.SetPolicy(ctx, ntd.R)     // 只读
ws.SetPolicy(ctx, ntd.RAsk)  // 所有操作需确认
```

`WAsk/RAsk` 模式下，引擎通过 bus 发出 `policy.ask` 请求，下游订阅后决定是否批准。

## 事件总线

```go
bus := ws.Bus()
bus.Subscribe("policy.ask", func(ctx context.Context, req any) (any, error) {
    // 展示给用户或 LLM，返回 policy.AskResponse{Approved: true/false}
})
bus.Publish(ctx, "my.topic", payload)
```

Bus 是进程内通道，适合 LLM pipeline 和 UI 层挂载。跨进程场景由下游自行桥接。
