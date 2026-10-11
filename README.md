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

Bus 提供两种模式：

**单向广播**（fire-and-forget）

```go
bus := ws.Bus()

_, unsub := bus.Subscribe("my.topic", func(payload any) {
    fmt.Println("received:", payload)
})
defer unsub()

bus.Publish("my.topic", "hello")
```

**请求/回复**（用于 `WAsk/RAsk` policy 确认）

```go
unsub := ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
    ask := req.(policy.AskPayload)
    // 展示给用户或 LLM 决策
    approved := askUser(ask.Op, ask.Path)
    return policy.AskResponse{Approved: approved}, nil
})
defer unsub()
```

### 引擎保留 topic

| Topic | 触发时机 | Payload 类型 | 返回类型 |
|---|---|---|---|
| `policy.ask` | policy 为 `WAsk`/`RAsk` 时每次操作前 | `policy.AskPayload` | `policy.AskResponse` |
| `procedure.start` | procedure 开始执行时 | `ntd.ProcedureEvent` | — |
| `procedure.done` | procedure 执行成功时 | `ntd.ProcedureEvent` | — |
| `procedure.error` | procedure 执行失败时 | `ntd.ProcedureEvent`（含 Err）| — |

`ntd.ProcedureEvent` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Name` | string | procedure 名称 |
| `BizmodelID` | string | 所属 bizmodel ID |
| `Err` | error | 仅 `procedure.error` 时非 nil |

`ntd.UpdateEvent` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Path` | string | 变更节点路径，如 `/notes/hello.md` |
| `Op` | string | `create` / `write` / `delete` / `rename` |

`policy.AskPayload` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `WorkspaceID` | string | 当前 workspace ID |
| `OwnerID` | string | 发起操作的 owner |
| `Op` | string | 操作名，如 `dir-mkdir` |
| `Path` | string | 操作路径 |
| `Preview` | string | 操作内容摘要（可选） |

`policy.AskResponse` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `Approved` | bool | `true` 放行，`false` 拒绝 |

> **重要**：使用 `WAsk` 或 `RAsk` 模式时，必须通过 `bus.Handle("policy.ask", ...)` 注册 handler，否则所有操作都会被直接拒绝。

Bus 是进程内通道，适合 LLM pipeline 和 UI 层挂载。跨进程场景由下游自行桥接。

## 下游接入：LLM + UI 双管线典型架构

ntd 只负责存储和权限，LLM 调用和 UI 渲染由下游应用组合。典型结构如下：

```
myapp/
├── main.go          # 组装入口
├── llm/             # LLM pipeline
│   └── agent.go
└── ui/              # UI / API 层
    └── handler.go
```

**组装入口 `main.go`**

```go
ws, _ := ntd.OpenWorkspace(ctx, "ws-1", "owner", ntd.Config{
    Meta: ntd.SqliteConfig{Path: "./meta.db"},
})

bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
    ID: "docs", TypeName: ntd.BackendLocalFile,
    Config: ntd.LocalFileConfig{RootDir: "./docs"},
})

// 挂载 LLM pipeline
llm.Mount(ws, bm)

// 挂载 UI 层（HTTP / WebSocket 等）
ui.Mount(ws, bm)
```

**LLM pipeline `llm/agent.go`**

LLM 通过 `bm.Call` 写入数据，通过 `bm.Tree()` 读取上下文，通过 bus 接收或发出事件。

```go
func Mount(ws *ntd.Workspace, bm *ntd.Bizmodel) {
    ownerCtx := ws.Context(context.Background())

    // 订阅 UI 发来的用户指令
    ws.Bus().Subscribe("user.instruction", func(payload any) {
        instruction := payload.(string)
        // 调用 LLM，把结果写回树
        result := callLLM(instruction, bm.Tree().Search(ownerCtx, "*"))
        bm.Call(ownerCtx, "file-fm-write", "/output/latest.md", map[string]any{
            "withfm": true,
            "fm":     map[string]any{"title": "LLM Output"},
            "body":   result,
        })
        // 通知 UI 更新
        ws.Bus().Publish("llm.done", result)
    })
}
```

**UI 层 `ui/handler.go`**

UI 接收用户输入，通过 bus 转发给 LLM，同时响应 `policy.ask` 确认请求。

```go
func Mount(ws *ntd.Workspace, bm *ntd.Bizmodel) {
    // 处理 policy 确认弹窗
    ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
        ask := req.(policy.AskPayload)
        approved := showConfirmDialog(ask.Op, ask.Path) // 展示给用户
        return policy.AskResponse{Approved: approved}, nil
    })

    // 订阅 LLM 完成事件，推送到前端
    ws.Bus().Subscribe("llm.done", func(payload any) {
        pushToFrontend(payload.(string))
    })

    // HTTP handler：用户输入 → bus → LLM
    http.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
        instruction := r.FormValue("q")
        ws.Bus().Publish("user.instruction", instruction)
        w.WriteHeader(http.StatusAccepted)
    })
}
```

**数据流**

```
用户输入
  → UI Publish("user.instruction")
    → LLM Subscribe，调用 bm.Call 写树
      → policy WAsk 触发 bus.Request("policy.ask")
        → UI Handle 弹窗确认 → 放行
      → 写入成功，Publish("llm.done")
        → UI Subscribe，推送前端
```

ntd 在这个架构里只提供树操作和权限门控，LLM SDK 和 UI 框架的选型完全由下游决定。

## 下游接入：自定义 Backend 示例（WebSearch）

以搜索结果作为只读树节点为例，展示如何把外部数据源接入 ntd。

**定义 Config 和 Backend**

```go
package websearch

import (
    "context"
    "encoding/json"
    "fmt"
    "ntd"
)

type Config struct {
    APIKey   string
    Endpoint string
}

func (Config) IsBackendConfig() {}

type Backend struct{}

func (Backend) Name() string { return "web_search" }

func (Backend) SubKinds() []ntd.SubKind {
    return []ntd.SubKind{
        {Name: "result", Description: "搜索结果节点", Ops: []string{"result-search", "result-open"}},
    }
}

func (Backend) OpsForSubKind(kind string) []ntd.ExternalOp {
    if kind != "result" {
        return nil
    }
    return []ntd.ExternalOp{
        {
            Name:        "result-search",
            Description: "执行搜索，把结果写入树",
            Schema: map[string]any{
                "type":     "object",
                "required": []string{"query"},
                "properties": map[string]any{
                    "query": map[string]any{"type": "string"},
                },
            },
            Op: func(ctx context.Context, t ntd.Tree, args ntd.OpArgs) (any, error) {
                m, _ := args.Args.(map[string]any)
                query, _ := m["query"].(string)
                // 调用搜索 API，把结果写入树（通过 t 的具体实现）
                return doSearch(ctx, query)
            },
        },
    }
}

func (Backend) NewTree(cfg ntd.BackendConfig) (ntd.Tree, error) {
    c, ok := cfg.(Config)
    if !ok {
        return nil, fmt.Errorf("websearch: invalid config")
    }
    return &searchTree{apiKey: c.APIKey, endpoint: c.Endpoint}, nil
}

func (Backend) ConfigFromJSON(data []byte) (ntd.BackendConfig, error) {
    var c Config
    return c, json.Unmarshal(data, &c)
}
```

**注册并使用**

```go
func init() {
    ntd.RegisterBackend(websearch.Backend{})
}

// 创建 bizmodel
bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
    ID:       "search",
    TypeName: "web_search",
    Config:   websearch.Config{APIKey: "sk-xxx", Endpoint: "https://api.example.com"},
})

// 执行搜索 op
ownerCtx := ws.Context(ctx)
results, _ := bm.Call(ownerCtx, "result-search", "/search/go-generics", map[string]any{
    "query": "Go generics best practices",
})

// 读取搜索结果树
briefs, _ := bm.Tree().ListWithBrief(ownerCtx, "/search")
```

下游只需 `import "ntd"`，不需要接触任何引擎内部包。

## Procedure：业务层复合操作

Op 是 backend 提供的原子存储操作，Procedure 是下游注册的业务层复合操作。两者都受 policy 控制，都出现在 `bm.Ops()`/`bm.Procedures()` 供 LLM tool 动态生成。

```go
bm.RegisterProcedure(ntd.Procedure{
    Name:        "ppt-to-md",
    Description: "把 PPT 文件转换为 Markdown 并写入树",
    Schema: map[string]any{
        "type":     "object",
        "required": []string{"src", "dst"},
        "properties": map[string]any{
            "src": map[string]any{"type": "string"},
            "dst": map[string]any{"type": "string"},
        },
    },
    Fn: func(ctx context.Context, r ntd.BizmodelResolver, args any) (any, error) {
        m := args.(map[string]any)
        // 可通过 r.GetBizmodel 操作任意 bizmodel
        docs, _ := r.GetBizmodel(ctx, "docs")
        md := convertPPT(m["src"].(string))
        return docs.Call(ctx, "file-fm-write", m["dst"].(string), map[string]any{
            "withfm": true,
            "fm":     map[string]any{"title": filepath.Base(m["src"].(string))},
            "body":   md,
        })
    },
})

// 调用
bm.CallProcedure(ownerCtx, "ppt-to-md", map[string]any{
    "src": "slides.pptx",
    "dst": "/notes/slides.md",
})
```

`BizmodelResolver` 只暴露 `GetBizmodel`，procedure 无法访问 workspace 其他能力。

执行过程中引擎自动发布：
- `procedure.start` — 开始前
- `procedure.done` — 成功后
- `procedure.error` — 失败时（含 `Err` 字段）

**动态生成 LLM tools（Op + Procedure 统一）**

```go
for _, op := range bm.Ops() {
    tools = append(tools, makeTool(op.Name, op.Description, op.Schema,
        func(args map[string]any) (any, error) {
            return bm.Call(ownerCtx, op.Name, args["path"].(string), args)
        }))
}
for _, proc := range bm.Procedures() {
    tools = append(tools, makeTool(proc.Name, proc.Description, proc.Schema,
        func(args map[string]any) (any, error) {
            return bm.CallProcedure(ownerCtx, proc.Name, args)
        }))
}
```
