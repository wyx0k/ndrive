# ndrive 架构

> 名字灵感来自《高达 UC》的 NT-D（NewType Destroyer）：在权限触发时精准响应，把业务意图直接驱动到引擎。

ndrive 是面向 **LLM / Agent 场景的工具注册框架**。它不定义数据模型、不内置业务后端，只提供"把能力接进来"的骨架：注册、依赖排序、权限门控、事件广播、LLM schema 投影，以及工作区级别的文件系统契约。

---

## 1. 定位

| ndrive 负责 | ndrive 不负责 |
|---|---|
| 工具注册中心（`tools`）：注册、依赖拓扑排序、统一入口调用、LLM 视图投影 | 数据模型定义（tool 自己定） |
| 4 档 `permission` 门控 + 人审询问通道 | 任何具体存储后端的业务语义 |
| 进程内 `bus`：生命周期事件广播 + 同步请求 | 用户 / 会话 / 鉴权身份管理 |
| 工作区文件系统契约（`fs-api`）+ 一个本地目录实现 | 跨进程的权限询问、事件分发（`bus` 是进程内通道） |
| `meta` 持久化（workspace 归属 + permission binding） | 业务数据持久化 |

**外部系统负责**：定义数据模型、编写 tool、提供 `userID`、决定谁能调 Workspace 的结构 CRUD。

> **约定**：`userID` 的语义由外部系统保证全局唯一且语义一致（引擎内只是字符串标识，不区分 user / agent / service），引擎不做校验。

---

## 2. 设计原则

1. **框架，不是数据层** —— 引擎不规定数据模型（不是 tree、不是 table、不是 doc），tool 自己定
2. **单一执行入口** —— `Call` 是唯一的调用路径。AI 调用（拿 `Tools()` 的 schema 生成参数）和直接函数调用走的是同一段代码，权限门控与事件广播对两者**完全一致**
3. **单一投影** —— `Tools()` 是唯一的对外视图（id + description + json schema），没有第二套"UI 视图"
4. **依赖倒置** —— 工作区文件系统是**接口**（`fs.FileService`），实现方（`fs/local`、未来的 `fs-mem` / `fs-single-file`）指向接口；能力单元只依赖接口
5. **包间依赖单向，无循环** —— 见 §3
6. **权限统一在 `Call` 入口** —— 所有 tool 触发都过门控，且只在这一处
7. **引擎不做鉴权** —— Workspace / policy 的 CRUD 调用一律裸调，外部负责谁有权调

---

## 3. 分层与依赖方向

```
        ┌─────────────────────────────────────────────┐
        │  外部系统（agent / llm / ui / service）       │
        └───────────────────┬─────────────────────────┘
                            │  Call(ctx, id, args)
                            │  Tools() → json schema
   ┌────────────────────────▼──────────────────────────┐
   │  ndrive 根包（容器层 / 装配）                       │
   │  Workspace · OpenWorkspace · WorkspaceConfig      │
   │  只认 meta.Config / fs.Config 两个契约             │
   └──┬──────────┬──────────────┬────────────┬─────────┘
      │          │              │            │
      ▼          ▼              ▼            ▼
   tools    permission        meta          fs
      │          │              │            ▲
      │          ▼              │            │
      │         bus             │            │
      ▼          │              ▼            │
     bus         │        meta/sqlite    fs/local
                 │        meta/mysql
                 ▼
```

依赖清单（`→` 表示"依赖"）：

| 包 | 依赖 ndrive 内的包 | 说明 |
|---|---|---|
| `bus` | — | 只依赖 stdlib |
| `permission` | `bus` | 询问通道走 bus |
| `tools` | `bus`、`permission` | 门控 + 事件广播；通过 `tools.Context` 接口反向使用容器能力，但不 import 根包 |
| `fs` | — | 只依赖 stdlib，是纯契约包 |
| `fs/local` | `fs` | 实现方指向接口 |
| `meta` | `permission` | facade 直接用 `permission.Binding` 类型 |
| `meta/sqlite`、`meta/mysql` | `meta` | 后端实现 |
| `ndrive`（根） | `bus`、`permission`、`tools`、`meta`、`fs` | 装配层——**不 import `fs/local` / `meta/sqlite` / `meta/mysql`** |

> **根包为什么不认识具体实现**：后端由调用方通过配置对象传进来（`fs.Config` / `meta.Config`），配置对象自己负责初始化。新增一个后端 = 新增一个实现包 + 根包零改动。

**为什么 `tools` 不 import 根包**：`Workspace` 实现 `tools.Context` 接口（`Bus()` / `Checker()` / `API()`），`tools` 侧只认接口，根包在构造时把自己的实例注入——这样 `tools` 可以独立测试，也不会有循环依赖。

---

## 4. 包结构

**关键点：根包 `ndrive` 的源文件直接放在模块根目录**（Go 模块惯例：模块名 = 根包名 = `ndrive`）。

```
github.com/wyx0k/ndrive/          # 模块根 = 根包 ndrive
├── ndrive.go            #   package ndrive ── OpenWorkspace + 装配
├── workspace.go         #   package ndrive ── Workspace struct + 公共方法
├── config.go            #   package ndrive ── WorkspaceConfig（只含两个契约字段）
├── policy.go            #   package ndrive ── Workspace 上的 permission 管理
├── fsevents.go          #   package ndrive ── fs 变更事件 → bus 的桥接
│
├── bus/                 # package bus ── 事件总线
│   └── bus.go           #   Bus 接口 + 默认内存实现
│
├── permission/          # package permission ── 4 档权限 + Checker
│   └── permission.go    #   Mode / Binding / Checker / AskPayload / AskResponse
│
├── tools/               # package tools ★ 核心 ── 工具注册中心
│   ├── tools.go         #   Tool / ToolDef / DependentTool / ToolsAPI / Context
│   ├── registry.go      #   Register / RegisterAll / Resolve（拓扑排序 + 环检测）
│   ├── dispatch.go      #   Call / Tools（唯一执行入口 + 唯一投影）
│   └── builtin.go       #   内置 util.echo（opt-in，不自动注册）
│
├── fs/                  # package fs ★ fs-api 契约（只依赖 stdlib）
│   ├── fs.go            #   FileService / Config / Watcher / FileEntry / Event / 约定目录常量
│   └── local/           # package local ── fs-local 实现
│       ├── local.go     #   FileService 实现 + 沙盒路径校验 + 变更监听
│       ├── config.go    #   local.Config ── 自己初始化成 fs.FileService
│       ├── local_test.go#   基础读写 / 增删改查测试
│       ├── path_test.go #   越界 / 软链 / junction 测试
│       └── watch_test.go#   变更监听（新建目录 / 批量变更 / 约定目录）测试
│
├── meta/                # package meta ── 持久化抽象 + typed facade
│   ├── meta.go          #   Store 契约 + Config 契约
│   ├── keys.go          #   key 命名空间
│   ├── workspace.go     #   WorkspaceStore facade
│   ├── permission.go    #   PermissionStore facade
│   ├── sqlite/          #   SQLite 后端（GORM）+ sqlite.Config
│   └── mysql/           #   MySQL 后端（GORM）+ mysql.Config
│
├── examples/            # 可运行示例（local/* 与 saas/*）
├── e2e/                 # 端到端测试
├── go.mod
└── ARCHITECTURE.md      # 本文档
```

---

## 5. 核心抽象

### 5.1 Workspace（容器层主类型）

工作区句柄，同时实现 `tools.Context`：

```go
type Workspace struct { /* 私有字段 */ }

func (w *Workspace) ID() string
func (w *Workspace) UserID() string
func (w *Workspace) Bus() bus.Bus
func (w *Workspace) API() *tools.ToolsAPI
func (w *Workspace) Checker() permission.Checker
func (w *Workspace) FS() fs.FileService
func (w *Workspace) Context(ctx context.Context) context.Context
func (w *Workspace) Register(t tools.Tool) error
func (w *Workspace) Close() error
```

- `FS()` 暴露工作区文件系统，**返回的是接口**——调用方不关心底层是本地目录、内存还是对象存储
- `Close()` 依次：关闭文件系统（若实现 `Close() error`）→ 关闭 meta store
- `Context()` 把 `(wsID, userID)` 注入 context，下游用 `ndrive.FromContext(ctx)` 取回

### 5.2 WorkspaceConfig —— 两个开放契约

```go
type WorkspaceConfig struct {
    Meta meta.Config   // sqlite.Config / mysql.Config / 自研实现
    FS   fs.Config     // local.Config / 自研实现
}
```

契约定义在**叶子包**里（`meta.Config` / `fs.Config`），根包只持有接口：

```go
// fs 包
type Config interface {
    NewFileService(ctx context.Context) (FileService, error)
}

// meta 包
type Config interface {
    NewStore(ctx context.Context) (Store, error)
}
```

**配置对象自己负责初始化**：实现方把"怎么造出自己"这件事写在自己包里，根包不 switch、不认实现类型。新增后端 = 新增一个实现包，**根包零改动**。

> 早期版本用的是 sealed interface（`isFSConfig()` 之类的未导出标记方法），只有根包内的类型能实现——那等于把契约锁死在根包里，用户没法接自己的后端。改成开放契约后 `fs` / `meta` 两个包是纯契约包，根包只做装配。

`Meta` / `FS` 都是**必填**项：工作区必然带一个元数据库和一个文件系统（见 §5.3）。

### 5.3 fs-api —— 工作区文件系统契约

`fs` 包是纯契约包（只依赖 stdlib），定义工作区文件系统的抽象：

```go
// 约定目录名（相对 root）。每个 fs 实现都必须提供这两个目录。
const (
    TmpDir = ".tmp"   // 临时产物，可随时清理
    MemDir = ".mem"   // 跨会话保留的长期状态
)

// TopicChange 是变更事件在 bus 上的 topic（bus 不支持通配符订阅，
// 所有变更走同一个 topic，具体类型看 payload 里的 Op）。
const TopicChange = "fs.change"

type FileService interface {
    List(ctx context.Context, path string, page, pageSize int) (ListPage, error)
    Stat(ctx context.Context, path string) (FileEntry, error)
    Read(ctx context.Context, path string) ([]byte, error)
    Open(ctx context.Context, path string) (io.ReadCloser, error)
    Search(ctx context.Context, query string, contentSearch bool) ([]FileEntry, error)
    Write(ctx context.Context, path string, body []byte) (FileEntry, error)
    WriteStream(ctx context.Context, path string, r io.Reader) (FileEntry, error)
    Edit(ctx context.Context, path string, old, newText string, expectedModTime time.Time) (FileEntry, error)
    Delete(ctx context.Context, path string, recursive bool) error
}

// Config 是 fs 后端的配置契约：配置对象负责把自己初始化成 FileService。
type Config interface {
    NewFileService(ctx context.Context) (FileService, error)
}

// Watcher 是 FileService 的可选扩展能力（fs 包不依赖 bus）。
type Watcher interface {
    Events() <-chan Event
    Close() error
}
```

**约定目录是硬约束**：每个 fs 实现的根目录里都必须有 `/.tmp` 和 `/.mem`。`OpenWorkspace` 在构造完 fs 之后会 `Stat` 这两个目录，缺一个就 fail-fast——将来接入 `fs-mem` / `fs-single-file` 时，这条约束在接入点就被卡住，而不是等到上层读写时才出问题。

**fs-local（`fs/local`）** 是当前唯一的实现：

- 路径参数都是**相对 root 的相对路径**
- 两层沙盒校验：
  1. **词法校验** —— `filepath.Clean` 之后必须仍在 root 内，`".."` 之类越界写法直接拒绝（不做静默裁剪）
  2. **链接校验** —— 解开路径里的软链 / Windows junction 之后，真实路径必须仍在 root 内。只做词法校验是不够的：root 里放一个指向外部的链接，`"link/x"` 就能读到沙盒外的文件
- 刻意**不用** `filepath.EvalSymlinks`：它在 Windows 上不认 junction（`os.Lstat` 对 junction 不置 `ModeSymlink`），而 junction 恰恰是 Windows 上最常见的"目录指向别处"手法。这里改用 `os.Readlink` 判定
- `WithWatch(window)` 开启变更监听（基于 `go-filewatcher/v2`），事件路径转成相对 root 的沙盒路径，不泄露宿主机绝对路径

### 5.3.1 变更事件 → bus

`fs` 包**不依赖 bus**：变更事件通过可选的 `Watcher` 接口以 Go 原生 channel 暴露，由根包桥接（`fsevents.go`）：

```
fs/local 的 Events()  ──►  根包 bridgeFSEvents  ──►  bus.Publish("fs.change", fs.Event)
```

桥接是单向的、fire-and-forget：

- fs 实现不实现 `Watcher`（只读后端、单文件后端）→ 什么都不做，工作区照常工作
- fs 关闭 → 事件 channel 关闭 → 桥接 goroutine 自然退出

`fs.Event` 的 `Op` 会 JSON 化成字符串（`"created"` / `"modified"` / `"removed"` / `"renamed"`），过 bus 到 UI 时人眼可读。

**fs-local 的事件合并规则**（`go-filewatcher` 自带的防抖不能直接用）：

- 底层 watcher 的事件粒度比 `fs.Event` 细（一次写入可能是"创建 + 写入"两条），而且**会漏事件**：底层 watcher 是收到"目录创建"事件之后才把该目录挂上监听的，所以"目录建好之后、监听挂上之前"写进去的文件它永远看不到——`Write` 里的"建目录 + 写文件"是挨着执行的，正好落在这个窗口里
- 因此 fs-local 自己做合并：窗口内同一路径的多个底层事件合并成一条，**优先级 创建 > 删除 > 修改**（保住"这是新建文件"的语义）；遇到新目录就**补扫整棵子树**，把监听挂上之前写进去的文件补成创建事件
- 用库自带的防抖会有两个坑：全局防抖把同一批里**其它文件的事件全丢掉**；按路径防抖只保留最后一条，把"创建"抹成"修改"

### 5.4 Tool —— 唯一的可调用单元

```go
// Tool 是注册中心里唯一的可调用单元（一次 Call 对应一个 Tool）。
type Tool interface {
    ID() ToolID         // 全局唯一；注册后即调用时使用的名字
    Description() string
    Schema() map[string]any
    Read() bool          // true = 只读操作（走 CheckRead），false = 写操作（走 CheckWrite）
    Call(ctx context.Context, ws Context, args any) (any, error)
}
```

**没有容器层**。注册表就是一张扁平的 `map[ToolID]Tool`，`Call` / `Resolve` / 权限校验都按 tool id 操作。tool 的 `ID()` 直接就是注册表里的键，也是调用名——不存在"容器 id + 工具 id"拼接这回事。

多个 tool 需要共享状态时，由调用方在注册时用闭包捕获同一个实例（例如 `fsvc := ws.FS()` 之后让两个 tool 都闭包捕获 `fsvc`）。

**可选的 tool 级接口**：

```go
// DependentTool —— 依赖声明。
type DependentTool interface {
    Tool
    DependsOn() []ToolID
}
```

依赖是**tool 级**的：每个 tool 自己声明依赖谁，注册时校验 + 拓扑排序。

> 早期的 `StatefulTool`（`StartTool` / `StopTool` 生命周期）已删除：注册中心里没有任何一个 tool 需要被引擎"启停"，而这套生命周期让 `Register` 多了个 `cfg` 参数、让 `RegisterAll` 多了"逆序回滚"的复杂度。需要长生命周期的 tool 自己 `Close`（工具通常由调用方持有）。

**`ToolDef`** 是声明式 helper，实现 `Tool` 与 `DependentTool`：字段名带下划线后缀（`ID_` / `Description_` / `Schema_` / `Read_` / `Fn_` / `DependsOn_`）以避开接口 getter 的命名冲突。

### 5.5 ToolsAPI —— 唯一执行入口 + 唯一投影

```go
func (a *ToolsAPI) Register(t Tool) error
func (a *ToolsAPI) RegisterAll(ts []Tool) error
func (a *ToolsAPI) Resolve() ([]ToolID, error)    // tool 级拓扑序（依赖在前）
func (a *ToolsAPI) Call(ctx context.Context, id ToolID, args any) (any, error)
func (a *ToolsAPI) Tools() []ToolInfo
```

**`Call` 的 4 步**（唯一执行路径）：

1. **权限门控** —— 按 `tool.Read()` 路由 `CheckRead` / `CheckWrite`；被拒绝时**不发 start 事件**
2. 发 `tool.<id>.start` 事件（payload：`CallEvent{Args}`）
3. 调 `tool.Call(ctx, ws, args)`
4. 发 `tool.<id>.done` 或 `tool.<id>.error` 事件

**`Tools()`** 返回 LLM 视角的只读快照，按 tool id 字典序：`ToolInfo{ID, Description, Schema}`。它是**唯一**的投影——没有第二套视图，调用方拿到的 schema 直接喂给模型。

**注册语义**：

- `Register` 立即校验 id 合法 → 不重名 → `DependsOn` 已注册（fail-fast，不支持 forward ref）。任何一步失败 registry 保持不变
- `RegisterAll` 先把所有 tool 的依赖校验完，再做拓扑排序（含环检测），最后一次性 commit。校验不过则注册表完全不变
- `Resolve` 用 DFS 拓扑排序 + 环检测，返回 tool id 的执行顺序（依赖在前）；环错误是 typed error `*CycleError`，`errors.Is(err, ErrToolCycle)` 可统一判定

### 5.6 permission —— 4 档门控

```go
type Mode int
const (
    ModeWriteAsk Mode = iota  // 读放行；写发 permission.ask 询问（零值，默认档）
    ModeReadOnly              // 读放行；写一律拒绝
    ModeAskAll                // 读写都发 permission.ask 询问
    ModeWriteOnly             // 写放行；读一律拒绝
)
```

| mode | 读 op | 写 op |
|---|---|---|
| `ModeWriteAsk` | 放行 | 询问 |
| `ModeReadOnly` | 放行 | 拒绝 |
| `ModeAskAll` | 询问 | 询问 |
| `ModeWriteOnly` | 拒绝 | 放行 |

**这四档不是一条严格度链**——`ModeWriteOnly`（只写）比 `ModeReadOnly`（只读）"更严"吗？不一定，它们是正交的读写两个维度上的四象限。名字直接写清语义，不要按"松紧"排序理解。

绑定粒度是 `(wsID, userID) → Mode`，持久化在 `permission_binding/<wsID>/<userID>`。

**询问流程**：`Checker.ask` 通过 `bus.Request(ctx, "permission.ask", AskPayload{...})` 同步等 UI 端应答。无订阅者 / 超时 / 明确拒绝，一律转成 `ErrPermissionDenied`。

**fail-closed**：查不到绑定、模式值不认识，一律按拒绝处理（`ErrPermissionDenied`），不靠"默认值兜底放行"。

`Mode` 用 **iota 数值**持久化（`MarshalJSON` / `UnmarshalJSON` 走 int），避免 iota 顺序变动破坏历史数据。改名不改值：`0=写询问 1=只读 2=全询问 3=只写` 与历史数据一一对应。

### 5.7 bus —— 进程内事件总线

两个通道：

| 通道 | 接口 | 语义 |
|---|---|---|
| pub/sub | `Publish(topic, payload)` / `Subscribe(topic, handler) (subID, unsubscribe)` | `Publish` 不阻塞，handler 在调用方 goroutine 里**按订阅顺序同步执行** |
| req/resp | `Handle(topic, handler) (unsub, error)` / `Request(ctx, topic, req) (any, error)` | 一个 topic 只能有一个 handler；`Request` 同步等返回 |

`Request` **自身不开 goroutine 也不包装 ctx**——handler 是 ctx 传播的终点，必须自己 `listen ctx.Done()`；`Request` 在 handler 返回后检查 `ctx.Err()`，非 nil 就把 ctx 的错误包装进 `ErrRequestTimeout`。

### 5.8 meta —— 持久化抽象 + typed facade

```go
type Store interface {          // 通用 KV
    Get(ctx context.Context, key string) (Record, error)
    Put(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]Record, error)
    Close() error
}
```

所有 key 都从 `meta/keys.go` 导出，上层不拼字符串：

| key | 用途 |
|---|---|
| `workspace/<wsID>` | workspace 元信息（value 就是 userID 原文，不是 JSON） |
| `permission_binding/<wsID>/<userID>` | 权限绑定 |

后端：`meta/sqlite`（GORM + SQLite）、`meta/mysql`（GORM + MySQL）。

---

## 6. 数据流

### 6.1 调用一个 tool

```
调用方
  │  ws.API().Call(ctx, "store.localfs.list", args)
  ▼
ToolsAPI.Call
  ├─ 查注册表（按 tool id）→ 没有则 ErrToolNotFound
  ├─ permission 门控：tool.Read() ? CheckRead : CheckWrite
  │     └─ 拒绝 → 直接返错，不发事件
  ├─ bus.Publish("tool.store.localfs.list.start", CallEvent{Args})
  ├─ tool.Call(ctx, ws, args)
  └─ bus.Publish("tool.store.localfs.list.done" | ".error", CallEvent{Result|Err})
```

### 6.2 跨 tool 调用

tool 内部通过 `ws.API().Call(...)` 调另一个 tool——**走的是同一条路径**：同样过门控、同样广播事件。依赖顺序由 tool 自己的 `DependentTool` 声明，注册时校验 + 拓扑排序。

### 6.3 注册

```
ws.Register(tool)
  ├─ 校验 tool id 合法
  ├─ 校验不重名
  ├─ 校验 DependsOn 的 tool 已注册
  └─ commit 到注册表
```

### 6.4 权限询问（写操作 / 全询问档）

```
ToolsAPI.Call → CheckWrite → Checker.ask
  │  bus.Request(ctx, "permission.ask", AskPayload{WorkspaceID, UserID, Op})
  ▼
UI 端 Handle("permission.ask") → AskResponse{Approved}
  │
  └─→ 批准则继续执行；拒绝 / 无订阅者 / 超时 → ErrPermissionDenied
```

### 6.5 文件变更

```
fs/local 的底层 watcher
  │  （按路径合并 + 新目录补扫）
  ▼
fs/local Events()  ──►  根包 bridgeFSEvents  ──►  bus.Publish("fs.change", fs.Event)
  │
  ▼
UI / agent 运行时 Subscribe("fs.change", …)
```

---

## 7. 事件约定

| topic | payload | 时机 |
|---|---|---|
| `tool.<id>.start` | `CallEvent{ToolID, Op:"start", Args, Time}` | 门控通过后、执行前 |
| `tool.<id>.done` | `CallEvent{ToolID, Op:"done", Result, Time}` | 执行成功 |
| `tool.<id>.error` | `CallEvent{ToolID, Op:"error", Err, Time}` | 执行失败 |
| `permission.ask` | `AskPayload` → `AskResponse` | 权限询问（req/resp） |
| `fs.change` | `fs.Event{Op, Path, IsDir, Size, ModTime}` | 工作区文件发生变更 |
| 自定义 | 任意 | tool 内部 `ws.Bus().Publish` 自行约定 |

事件 topic 用 `tool.` 前缀，与 LLM 工具名 `<id>` 区分开——两者不在同一个命名空间里。

`fs.change` 是唯一的文件变更 topic：bus 不支持通配符订阅，具体变更类型（新建 / 修改 / 删除 / 重命名）看 payload 里的 `Op`。

---

## 8. 关键决策

| 决策 | 理由 |
|---|---|
| `Call` 是唯一执行入口，`Funcs()` 已删除 | AI 调用和函数调用曾经是两套入口，门控与事件语义容易漂移。合成一条路径后，两类调用者拿到完全一致的行为保证 |
| `Tools()` 是唯一投影 | 曾经有 LLM 视图和 UI 视图两套投影；实际只需要 schema 那一套，另一套没有独立价值 |
| 去掉 Plugin 容器层，Tool 是唯一可调用单元 | 容器层只带来两样东西：id 拼接规则和"共享状态的兄弟 tool"。前者让调用名变成两段、每次都要解释"完整 id 怎么来的"；后者用闭包捕获就够。层级少一层，注册表、投影、事件、权限四条链路全都变短，也少了一整类 id 语义 bug |
| fs-api 是接口而不是具体包 | 工作区文件系统是最容易换掉的依赖（本地目录 / 内存 / 单文件 / 对象存储）。依赖倒置之后，换实现不动上层 |
| 约定目录写进契约而不是写进实现 | `/.tmp` 和 `/.mem` 是工作区的结构性约定，不是某个后端的私有实现细节。写进接口并在接入点校验，新实现漏了就接不进来 |
| `Register` 失败时 registry 不变 | fail-fast + 原子性：配置错了就在注册时炸，而不是等到第一次调用才发现半个注册表是坏的 |
| 权限统一在 `Call` 入口 | 门控散落在各处时一定会漏。单一入口是唯一能保证"所有触发都过门控"的结构 |
| 引擎不做鉴权 | Workspace / policy 的 CRUD 是结构操作，引擎无权知道谁是谁。外部系统负责 |
| 后端配置走**开放契约**（`fs.Config` / `meta.Config`），不用 sealed interface | sealed interface 把契约锁在根包里，用户接不了自己的后端；配置对象自己初始化自己，根包只做装配——新增后端根包零改动 |
| 删掉 `StatefulTool` 生命周期层 | 注册中心里没有任何 tool 需要被引擎"启停"；这套生命周期给 `Register` 加了没人用的 `cfg` 参数，给 `RegisterAll` 加了"逆序回滚"的复杂度 |
| 权限四档改名（`RAsk`→`ModeWriteAsk` 等） | 老名字按"松紧"排序（`RAsk < R < WAsk < W`），但四档其实是读/写两个维度的四象限，不是链。名字直接写清语义 |
| fs 变更事件由**根包桥接**到 bus，`fs` 包不依赖 bus | 存储层不该知道消息总线存在；`fs` 保持纯契约包，桥接点集中在一个文件里 |
| fs-local 自己合并事件，不用 watcher 自带的防抖 | 全局防抖会把同一批里其它文件的事件全丢掉；按路径防抖只保留最后一条，把"新建文件"抹成"修改"。自己合并还能顺手补上"新目录里监听挂上前写的文件"这个盲区 |

---

## 9. 已知边界

- **`bus` 是进程内通道** —— 跨进程部署时，事件广播与 `permission` 询问都不自动 scale，需要下游自行替换 `bus.Bus` 实现
- **沙盒校验存在 TOCTOU 窗口** —— 校验与实际 `open` 之间，有本地写权限的攻击者可以把路径换成软链。要彻底消除需要 `O_NOFOLLOW` 之类的打开语义，跨平台不可得；当前只保证"稳态下不越界"
- **fs-mem / fs-single-file 尚未实现** —— 契约和接入点的校验都就位了，实现是独立的后续工作
- **变更事件有合并窗口（默认 500ms）** —— 窗口内同一路径的多个变更合并成一条；跨进程的增量同步需要更细的粒度时得另做
- **tool 拿不到工作区文件系统** —— `tools.Context` 只暴露 `Bus()` / `Checker()` / `API()`，没有 `FS()`。当前 tool 要用文件系统只能靠闭包捕获 `ws.FS()`；`func` 加载器落地时需要决定是把 `FS()` 提到 `tools.Context`（`tools → fs` 依赖成立），还是由加载器在注册时注入
- **`func` 加载器尚未实现** —— 现在只能用 `ToolDef`（Go 闭包）声明 tool；按约定加载器是独立的一块，不在当前范围内
