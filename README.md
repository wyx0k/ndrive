# NDrive

[English Version](README_en.md)

---

名称灵感来自《高达 UC》中的 NT-D（NewType Destroyer）系统——一套在特定条件下被激活、以极高精度响应意图的驱动系统。NDrive 取其意：在权限触发时精准响应，把业务意图直接驱动到执行层。

一个面向 LLM / Agent 场景的工具注册框架。把任意能力（存储、网络、本地工具……）包装成 tool 挂到 Workspace 上，统一走权限校验、事件广播，并自动生成 LLM tool schema。

```
go get github.com/wyx0k/ndrive
```

## 核心概念

| 概念 | 说明 |
|---|---|
| **Workspace** | 独立工作空间，绑定 userID，持有 meta 存储、事件总线、权限 Checker、工作区文件系统和工具注册中心 |
| **Tool** | 唯一可调用的单元：`ID`/`Description`/`Schema`/`Read` + `Call`；可选实现 `DependentTool`（声明依赖） |
| **ToolsAPI** | 工具注册中心：注册、依赖拓扑排序、权限门控调用、LLM 视图投影 |
| **fs-api** | 工作区文件系统契约（`fs.FileService`）：列举/读取/搜索/写入/编辑/删除 + 可选变更监听（`fs.Watcher`） |
| **Permission** | 四档权限：只读 / 只写 / 写询问 / 全询问，按 user 维度配置 |
| **Bus** | 进程内发布/订阅 + 同步请求通道 |

## 特性

- **单一执行入口**：`Call` 是唯一的调用入口——AI 调用（拿 `Tools()` 的 schema 生成参数）和直接函数调用走的是同一条路径，权限门控与事件广播对两者一视同仁
- **只有一层**：Tool 就是注册单位，没有容器层。tool 的 `ID()` 直接是注册表里的键，也是调用名；多个 tool 要共享状态时用闭包捕获同一个实例
- **依赖图 + 拓扑排序**：tool 之间用可选接口 `DependentTool`（`DependsOn() []ToolID`）声明依赖，注册时自动校验、批量注册时按 tool 自动排序
- **四档权限模型**：`ModeReadOnly`/`ModeWriteOnly`/`ModeWriteAsk`/`ModeAskAll`，后两档会通过 Bus 向外发起确认请求，适合需要人审的高风险操作
- **事件总线**：每次 Call 自动广播 `start`/`done`/`error` 生命周期事件（topic `tool.<id>.*`），tool 内部也能发自定义事件，方便 LLM pipeline 和 UI 层挂载
- **工作区自带文件系统**：每个 Workspace 挂一个 `fs.FileService`，实现方由用户选（当前只有本地目录 `fs/local`）；根目录里固定包含 `/.tmp`（临时产物）和 `/.mem`（长期记忆）两个约定目录
- **文件变更事件上总线**：开启监听后，工作区里的文件变更会以 `fs.change` 为 topic 广播到 Bus，UI / agent 运行时直接订阅即可

## 快速开始

```go
import (
    "context"

    "github.com/wyx0k/ndrive"
    "github.com/wyx0k/ndrive/fs/local"
    "github.com/wyx0k/ndrive/meta/sqlite"
    "github.com/wyx0k/ndrive/permission"
    "github.com/wyx0k/ndrive/tools"
)

ctx := context.Background()

ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
    Meta: sqlite.Config{Path: "./meta.db"},
    FS:   local.Config{Path: "./ws"},
})
if err != nil {
    // ...
}
defer ws.Close()

// 注册一个最简单的 tool
ws.Register(tools.ToolDef{
    ID_:          "util.echo",
    Description_: "原样返回 args",
    Read_:        true,
    Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
        return args, nil
    },
})

// 授权后调用
ws.SetPolicy(ctx, "alice", permission.ModeWriteOnly)
result, _ := ws.API().Call(ctx, "util.echo", "hello")
```

后端由调用方通过配置对象传进来——`sqlite.Config` / `mysql.Config` / `local.Config` 各自负责把自己初始化成对应后端，也可以换成自己的实现（`meta.Config` / `fs.Config` 是开放契约，根包不认识任何具体实现）。

---

## 当前架构

### 分层

依赖单向，无循环：

```
外部系统（agent / llm / ui）
   ↓ Call(ctx, id, args)
ndrive 根包（纯装配：OpenWorkspace + Workspace）
   ├── bus        进程内 pub/sub + req/resp
   ├── permission 4 档门控 + 人审询问（走 bus）
   ├── tools      注册中心：注册 / 拓扑排序 / 唯一执行入口 / LLM 投影
   ├── meta       KV 持久化 + typed facade（workspace 归属、permission binding）
   └── fs         文件系统契约 + 约定目录 /.tmp、/.mem
```

根包**不认识任何具体实现**——`meta` / `fs` 两个后端由调用方通过配置对象传进来，配置对象自己初始化自己。新增一个后端 = 新增一个实现包，根包零改动。

### 三条核心链路

| 链路 | 入口 | 说明 |
|---|---|---|
| 调用 | `ws.API().Call(ctx, id, args)` | 唯一执行路径：权限门控 → `tool.<id>.start` → 执行 → `done`/`error`。AI 调用和直接函数调用走同一条 |
| 投影 | `ws.API().Tools()` | 唯一 LLM 视图（id + description + json schema），按 id 字典序 |
| 事件 | `ws.Bus()` | `tool.<id>.*` 生命周期、`permission.ask` 同步询问、`fs.change` 文件变更 |

### 权限四档

`Mode` 用 iota 数值持久化，改名不改值（历史数据不受影响）。这四档是**读 / 写两个维度上的四象限，不是一条严格度链**——谁更严取决于操作。

| mode | 读 op | 写 op |
|---|---|---|
| `ModeWriteAsk`（零值默认档） | 放行 | 询问 |
| `ModeReadOnly` | 放行 | 拒绝 |
| `ModeAskAll` | 询问 | 询问 |
| `ModeWriteOnly` | 拒绝 | 放行 |

门控 **fail-closed**：查不到绑定、模式值不认识，一律按拒绝处理（`permission.ErrPermissionDenied`），不靠"默认值兜底放行"。

---

## 下游接入

### 1. 接自己的存储后端

写一份 `Config` 实现契约即可，根包零改动：

```go
// 实现 meta.Config
type MyMetaConfig struct{ ... }
func (c MyMetaConfig) NewStore(ctx context.Context) (meta.Store, error) { ... }

// 实现 fs.Config —— 必须保证 /.tmp、/.mem 存在，否则 OpenWorkspace fail-fast
type MyFSConfig struct{ ... }
func (c MyFSConfig) NewFileService(ctx context.Context) (fs.FileService, error) { ... }
```

然后照常传进 `WorkspaceConfig{Meta: ..., FS: ...}`。

### 2. 订阅事件

UI / agent 运行时直接订阅：

```go
ws.Bus().Subscribe(fs.TopicChange, func(p any) { /* fs.Event */ })
ws.Bus().Subscribe("tool.util.echo.done", func(p any) { /* tools.CallEvent */ })
```

topic 约定：

| topic | payload | 时机 |
|---|---|---|
| `tool.<id>.start` / `.done` / `.error` | `tools.CallEvent` | 每次 Call 自动广播 |
| `permission.ask` | `AskPayload` → `AskResponse` | 权限询问（req/resp） |
| `fs.change` | `fs.Event` | 工作区文件发生变更 |

bus 不支持通配符订阅，所以文件变更只有一个 topic，具体类型看 payload 里的 `Op`（`created` / `modified` / `removed` / `renamed`）。

### 3. 接人审确认

订阅 `permission.ask` 并回 `AskResponse{Approved}`：

```go
ws.Bus().Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
    p := req.(permission.AskPayload)
    return permission.AskResponse{Approved: askHuman(p)}, nil
})
```

`Handle` 的 handler 是 ctx 传播的终点，需要自己 `listen ctx.Done()`。

### 4. tool 之间互相调用

`tools.Context` 里带 `Bus()` / `Checker()` / `API()`，直接 `ws.API().Call(...)` 即可——同样过门控、同样广播事件。

### 5. 拿工作区文件系统

`ws.FS()` 返回 `fs.FileService` 接口——实现方是本地目录、内存还是自研，调用方不关心：

```go
fsvc := ws.FS()

// 写一个文件
entry, _ := fsvc.Write(ctx, "notes/hello.txt", []byte("hello ndrive"))

// 读回来
data, _ := fsvc.Read(ctx, "notes/hello.txt")
```

注意 `tools.Context` 里**没有** `FS()`——tool 要用文件系统得在注册时闭包捕获：

```go
fsvc := ws.FS()
ws.Register(tools.ToolDef{
    ID_: "store.localfile.read", Description_: "读文件", Read_: true,
    Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
        return fsvc.Read(ctx, pathFrom(args))
    },
})
```

更完整的可运行示例见 [`examples/local/basic_workspace`](examples/local/basic_workspace/main.go)（Workspace 生命周期）、[`examples/local/tool_registration`](examples/local/tool_registration/main.go)（注册与调用）、[`examples/local/storage`](examples/local/storage/main.go)（用工作区文件系统读写文件）、[`examples/local/events`](examples/local/events/main.go)（事件总线）和 [`examples/local/permission`](examples/local/permission/main.go)（四档权限）。

## 文档

- [架构说明](ARCHITECTURE.md)
- [实施 Specs](SPECS.md)
