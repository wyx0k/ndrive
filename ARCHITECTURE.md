# ndrive 架构 (v5)

> 名字灵感来自《高达 UC》的 NT-D（NewType Destroyer）：在权限触发时精准响应，把业务意图直接驱动到引擎。
> v5 把 ndrive 从"树形业务模型存储引擎"重定位为**插件注册框架**——引擎不规定数据模型，plugin 自己定义。

---

## 1. 设计原则

1. **ndrive 是框架，不是数据层** —— 引擎不规定数据模型（不是 tree、不是 table、不是 doc），plugin 自己定
2. **统一 plugin 机制** —— `Plugin` 接口 + `SimplePluginDef` helper，所有能力走同一通道
3. **注册一次，双视图派生** —— Tool（LLM 视角）和 Func（UI 视角）从同一注册表派生
4. **3 个高内聚低耦合顶层子包 + 根包容器** —— `bus` / `permission` / `tools` 三个独立子包 + 根 `ndrive` 本身作为容器层（不是薄壳）；`id` / `meta` 降为 `ndrive/internal`
5. **无内置数据后端** —— v4 的 5 个 backend 全部移除；用户用 `Plugin` 接口自己写
6. **包间依赖单向，无循环** —— `bus ← permission ← tools ← ndrive`；tools 通过 `Context` 接口反向使用容器能力，但不 import ndrive
7. **permission 统一在 `ToolsAPI.Call` 入口** —— 所有 plugin 触发都过门控
8. **引擎不做鉴权** —— Workspace / policy 的 CRUD 调用一律裸调，外部负责谁有权调

---

## 2. 系统定位

| ndrive 负责 | ndrive 不负责 |
|---|---|
| 插件注册中心（`tools`） | **数据模型定义**（plugin 自己定）|
| 4 档 `permission` 门控 | **任何具体存储后端**（v4 的 5 个 backend 全部移除）|
| 进程内 `bus` | 业务逻辑实现（用户写 `SimplePluginDef.Fn_`）|
| 进程级 snowflake ID 工厂（`ndrive/internal/id`）| 用户/会话/鉴权身份管理 |
| `meta` 持久化（`ndrive/internal/meta`，存 workspace / permission / plugin 3 类 key）| tree / node / 任何层级数据模型 |
| 根包入口（`OpenWorkspace`）| tree / node / 任何层级数据模型 |

**外部系统负责**：定义数据模型、选/写 plugin、提供 `user_id`、决定谁能调结构 CRUD。

> **约定**：`user_id` 的语义由外部系统保证全局唯一且语义一致（引擎内只是字符串标识，不区分 user / agent / service），引擎不做任何校验。`bus` 是单进程通道，跨进程部署时冲突检测与 `permission` 询问均不自动 scale，需下游自行替换 bus 实现。

---

## 3. 分层架构

| 层 | 名称 | 路径 | 是否在引擎内 check permission |
|---|---|---|:-:|
| L4 | 外部系统 | - | - |
| L3 | 智能体 | `agent / llm / tools` [下游] | - |
| L2 | 表现 | `service / ui` [下游] | - |
| L1 | 控制 | `permission` + `bus` | - |
| ★ | 插件注册中心 | `tools`（`Plugin` / `SimplePluginDef` / `ToolsAPI`） | ✅ `Call` 入口门控 |
| L0 | 工作区 | `ndrive` 根包（`Workspace` / `OpenWorkspace` / `MetaConfig`）| ❌ 结构 CRUD 裸调 |
| -1 | 基础 | `ndrive/internal/{id,meta}` | ❌ 基础设施 |

**依赖方向**（严格自上而下）：

```
                ┌─────────────────────────────┐
                │       ndrive（根）        │
                │  Workspace / OpenWorkspace │
                │  MetaConfig / Sqlite / ... │
                │  internal/id  internal/meta│
                └──────────┬──────────┬──────┘
                           │          │
                           ▼          ▼
                     ┌────────┐  ┌─────────┐
                     │  bus   │◀─│  tools  │──▶ permission ──▶ bus
                     └────────┘  └─────────┘
```

- 根 `ndrive` 直接依赖 `bus` / `tools` / `permission`，把三者组装成 `Workspace`
- `tools` 依赖 `permission` 做门控，依赖 `bus` 发事件
- `tools` 通过 `tools.Context` 接口反向使用容器能力（`Bus()`），但不 import ndrive，避免循环
- `permission` 依赖 `bus` 发 `permission.ask` 请求
- `bus` 不依赖任何 ndrive 包
- `internal/` 目录强制外部不可 import

### 鉴权边界表

| 操作 | 引擎内 `permission.Check`? | 理由 |
|---|:-:|---|
| `workspace.Create / Close` | ❌ | 结构操作 |
| `workspace.SetPolicy / GetPolicy / ListPolicies / DeletePolicy` | ❌ | policy 本身是配置 |
| `meta.Put / Get / Delete / List` | ❌ | 基础设施 |
| `bus.Publish / Subscribe / Request` | ❌ | 基础设施 |
| **`tools.ToolsAPI.Call(ctx, id, args)`**（任意 plugin）| ✅ | 写 plugin 是 LLM 主要约束对象 |

> v4 还有 `CreateBizmodel / DeleteBizmodel` 等结构 CRUD —— v5 整个 Bizmodel 概念移除，不再有这一行。

---

## 4. 包结构

**关键点：根包 `ndrive` 的源文件直接放在模块根目录**（Go 模块惯例：模块名 = 根包名 = `ndrive`，所有 `package ndrive` 的 `.go` 文件在模块根）：

```
github.com/wyx0k/ndrive/          # 模块根 = 根包 ndrive
├── ndrive.go            # package ndrive ── OpenWorkspace / Workspace / WorkspaceConfig / MetaConfig
├── internal/            # 根包 ndrive 的内部子包（外部不可 import）
│   ├── id/              # snowflake 节点（私有）
│   │   └── id.go        #   package id
│   └── meta/            # GORM sqlite/mysql 存储（私有）
│       └── meta.go      #   package meta
│
├── bus/                 # 顶层子包 1（package bus）
│   ├── bus.go           #   Bus 接口 + 默认实现
│   └── ...
│
├── permission/          # 顶层子包 2（package permission，重命名自 v4 policy）
│   ├── permission.go    #   Mode / Binding / Checker / AskPayload / AskResponse
│   └── ...
│
├── tools/               # 顶层子包 3 ★ 核心（package tools）
│   ├── tools.go         #   Plugin / SimplePluginDef / ToolsAPI / Tool / Func / PluginID / Context
│   ├── builtin/         #   内置 plugin 提供方（v5 起步为空）
│   │   └── builtin.go   #     package builtin
│   └── ...
│
├── go.mod               # module github.com/wyx0k/ndrive
├── ARCHITECTURE.md      # 本文档（v5）
└── ARCHITECTURE.v4.md   # v4 归档
```

**为什么根包 `ndrive` 的 `.go` 文件必须在模块根**：
- Go 模块的 import 路径 = 目录路径；`github.com/wyx0k/ndrive` 只能 import 模块根目录里的 `package ndrive` 源文件
- 如果 `ndrive` 包放在 `ndrive/` 子目录里，模块根就没有名为 `ndrive` 的包，用户就 `import "github.com/wyx0k/ndrive"` 拿不到任何东西
- v4 现状就是根包文件在模块根（`ntd.go` / `workspace.go` 等直接放在根），v5 只是把 `package ntd` 改成 `package ndrive` + 把功能拆给 3 个子包

### 与 v4 路径对照

| v4 路径 | v5 路径 | 变化 |
|---|---|---|
| `pkg/bus/` | `bus/` | 顶层化，路径短了 |
| `pkg/policy/` | `permission/` | 重命名 + 顶层化 |
| `pkg/id/` | `ndrive/internal/id/` | **降为 internal** |
| `pkg/tree/` | **删除** | tree 概念整体移除 |
| `pkg/backend/` + 5 子包 | **删除** | 5 backend 全部移除 |
| `pkg/subkind/` | **删除** | subkind 概念移除 |
| `pkg/meta/` | `ndrive/internal/meta/` | 顶层化到 internal |
| `pkg/ctx/` | 各包内私有 | ctx keys 跟着使用方走 |
| 根 `ntd` 包（含 Workspace/Tree/PolicyMode/Bizmodel 等）| 根 `ndrive` 包（容器层 + 原 workspace 内容，**Bizmodel 移除**）| **根包就是容器，不再拆出 workspace** |

---

## 5. 顶层包设计

### 5.1 `bus`

**职责**：进程内事件传递的单点。

**公共 API**：

```go
package bus

type Bus interface {
    Publish(topic string, payload any)
    Subscribe(topic string, handler func(any)) (subID uint64, unsubscribe func())
    Handle(topic string, handler func(ctx context.Context, req any) (any, error)) (unsub func(), err error)
    Request(ctx context.Context, topic string, req any) (any, error)
}

func New() Bus

var (
    ErrNoSubscriber      = errors.New("no subscriber")
    ErrHandlerExists     = errors.New("handler already registered for topic")
    ErrRequestTimeout    = errors.New("request timeout")
)
```

**v5 修订点**（walkthrough round 1 已定）：

1. **`Request` 走方案 B** —— 不开 goroutine，不包装 ctx；直接把 `ctx` 传给 handler，依赖 handler 内部 `select { case <-ctx.Done(): ... }` 监听取消。`Request` 自身只在 handler 返 error 或 `ctx.Err()` 非 nil 时返 `ErrRequestTimeout`
   - 理由：零 goroutine 泄漏；handler 知道自己的语义（DB 查询 / HTTP / 业务逻辑），自己最清楚怎么 listen
2. **`Handle` 重复注册返 `ErrHandlerExists`** —— 不 panic 也不覆盖；保留先注册的 handler
   - 理由：严格（"重复是 bug"）但不爆炸；测试场景如果想替换 handler 必须先调 `unsubscribe()`
3. **`New(int)` → `New()`** —— hard break，v4 `bus.New(100)` 在 v5 编译不过
   - 理由：v4 参数无效，纯误导；hard break 已在 plan §12 明确
4. **不变**：`Publish` / `Subscribe` 主体

**不规定**：分布式支持、跨进程消息、持久化订阅。v5 仍是单进程。

**外部 import**：`github.com/wyx0k/ndrive/bus`

**walkthrough 决议记录**：
- ✅ ctx 超时：方案 B（不开 goroutine）
- ✅ Handle 重名：返 error + 保留旧的
- ✅ New 参数：hard break 去掉

---

### 5.2 `permission`

**职责**：谁能调什么的判定中心。

**公共 API**：

```go
package permission

type Mode int
const (
    RAsk Mode = iota
    R
    WAsk
    W
)

type Binding struct {
    WorkspaceID string
    UserID      string   // v5: 改名 owner_id → user_id
    Mode        Mode
}

type AskPayload struct {
    WorkspaceID string
    UserID      string   // v5: 改名 owner_id → user_id
    Op          string
    Preview     string   // v5: 允许不填（默认空字符串）
}

type AskResponse struct {
    Approved bool
}

type Checker interface {
    CheckRead(ctx context.Context, op string) error
    CheckWrite(ctx context.Context, op string) error
}

type WorkspaceStore interface {
    GetBinding(ctx context.Context, wsID, userID string) (Binding, error)
    SetBinding(ctx context.Context, b Binding) error
    DeleteBinding(ctx context.Context, wsID, userID string) error
    ListBindings(ctx context.Context, wsID string) ([]Binding, error)
}

// 每个 user 一个 checker 实例（userID 在构造时绑定）
func NewChecker(store WorkspaceStore, wsID, userID string, b bus.Bus) Checker
```

**v5 修订点**（walkthrough round 2 已定）：

1. **`owner_id` → `user_id`** —— 术语更精准；整个仓库（不仅 permission 包）的 `OwnerID` 字段都改名 `UserID`
2. **`Checker` 拆成 `CheckRead` / `CheckWrite` 两个方法**（方案 B）
   - 调用方在 `ToolsAPI.Call` 内部根据 `plugin.Read()` 路由：`plugin.Read() → c.CheckRead`，否则 `c.CheckWrite`
   - 编译器强制调用方想清楚（不能传错 `read` 参数）
   - **Plugin 不直接调 `CheckRead` / `CheckWrite`**（这些是引擎在 `ToolsAPI.Call` 入口调用的，plugin 只声明 `Read() bool` 即可）
3. **`Binding` 持久化** —— 注入 `WorkspaceStore` 给 `NewChecker`，permission 包内部负责 `Set/Get/List/Delete`（v4 是顶层散落函数）
4. **每 user 一个 mode** —— `Binding` 按 `(wsID, userID)` 唯一索引；一个 workspace 可有 N 个 user，每个 user 自己的 mode
   - `Workspace.SetPolicy(ctx, userID, mode)` —— 必须传 userID
   - `Workspace.GetPolicy(ctx, userID)` —— 必须传 userID
   - `Workspace.ListPolicies(ctx)` —— 返该 workspace 下所有 `(userID, mode)` 对
   - `Workspace.DeletePolicy(ctx, userID)` —— 必须传 userID
5. **`AskPayload.Preview` 允许不填** —— v4 字段存在但无 producer；v5 明确允许空字符串，UI 端渲染"无预览"
6. **无人订阅时** —— 返 `ErrPermissionDenied: no subscriber`（拒绝执行，沿用 v4 行为）
7. **`bus.Request` ctx 超时** —— permission.ask 的 handler 必须自己 listen ctx 返 `ErrRequestTimeout`；permission 收到后转成 `ErrPermissionDenied: timeout`
8. **移除 `pkg/policy.Bus` 重复 interface** —— 直接 import `bus` 顶层包（v4 重复定义是因为不想依赖顶层 bus；v5 顶层 bus 已是稳定包）

**决策矩阵**（`CheckRead` / `CheckWrite` 走不同分支）：

| mode | CheckRead | CheckWrite |
|---|:-:|:-:|
| RAsk | ✅ 直接放行 | ❓ 发 `permission.ask` 请求 |
| R | ✅ 直接放行 | ❌ 拒绝 |
| WAsk | ❓ 发 `permission.ask` 请求 | ❓ 发 `permission.ask` 请求 |
| W | ❌ 拒绝 | ✅ 直接放行 |

**外部 import**：`github.com/wyx0k/ndrive/permission`

**walkthrough 决议记录**：
- ✅ Checker 接口：方案 B（`CheckRead` / `CheckWrite` 拆分）
- ✅ Plugin 不直接调 Checker（`ToolsAPI.Call` 引擎按 `plugin.Read()` 路由）
- ✅ AskPayload.Preview：允许空字符串
- ✅ 无订阅者：返 `ErrPermissionDenied`
- ✅ ctx 超时：handler 内部 listen，permission 转译为 `ErrPermissionDenied`
- ✅ user_id 命名 + 每 user 一个 mode
- ✅ 一致性影响：`Workspace` 层的 `SetPolicy/GetPolicy/ListPolicies/DeletePolicy` 全要带 `userID`

---

### 5.3 `tools` ★ 核心

**职责**：所有功能插件的注册表 + 派发器。

**公共 API**：

```go
package tools

// PluginID 全局唯一 plugin ID
// 约定 "xx.xx.xx..." 点分形式，>=2 段
// 命名空间建议：store.* / func.* / util.* / 自定义（如 user.myapp.search）
// 不强制 plugin. 前缀
type PluginID = string

// Context 是 tools 包对工作区所需的最小接口
// 由 ndrive.Workspace 实现；tools 不反向依赖 ndrive（避免循环 import）
type Context interface {
    Bus() bus.Bus
    // 未来扩展：Logger() / Metrics() / Config() 等
}

// Plugin 插件接口
type Plugin interface {
    ID() PluginID
    Description() string
    Schema() map[string]any
    DependsOn() []PluginID
    Read() bool
    Call(ctx context.Context, ws Context, args any) (any, error)
}

// SimplePluginDef helper struct（声明式描述 plugin）
// 用 struct + getter 让用户免手写 Plugin 接口 5 个方法
type SimplePluginDef struct {
    ID_         PluginID
    Description_ string
    Schema_     map[string]any
    DependsOn_  []PluginID
    Read_       bool
    Fn_         func(ctx context.Context, ws Context, args any) (any, error)
}
// 5 个 getter 自动实现 Plugin

// ToolsAPI 注册中心
type ToolsAPI struct { /* ... */ }

func NewToolsAPI(ws Context) *ToolsAPI
func (api *ToolsAPI) Register(plugin Plugin) error
func (api *ToolsAPI) RegisterAll(plugins []Plugin) error   // 批量注册：按 DependsOn 排序后逐个 Register
func (api *ToolsAPI) Resolve() ([]PluginID, error)
func (api *ToolsAPI) Call(ctx context.Context, id PluginID, args any) (any, error)
func (api *ToolsAPI) Tools() []Tool
func (api *ToolsAPI) Funcs() []Func

// 错误
var (
    ErrPluginNotFound  = errors.New("plugin not found")
    ErrPluginDuplicate = errors.New("plugin already registered")
    ErrPluginCycle     = errors.New("plugin dependency cycle")
)

type CycleError struct {
    Cycle []PluginID  // [a, b, c, a] —— 环的完整路径
}
func (e *CycleError) Error() string
// 调用方用 errors.As(err, &cycleErr) 拿环路径；errors.Is(err, ErrPluginCycle) 判断是否是环

// 双视图投影
type Tool struct {
    ID          PluginID
    Description string
    Schema      map[string]any
}
type Func struct {
    ID   PluginID
    Call func(ctx context.Context, args any) (any, error)
}
```

**v5 关键点**（walkthrough round 3 已定）：

1. **数据模型归零**：tools 不规定 plugin 操作什么（不是 tree，不是 table，不是 doc —— plugin 自己定）
2. **依赖图 + RegisterAll**：
   - 单 `Register(plugin)` 仍可用（用户按依赖顺序写）
   - `RegisterAll([]Plugin)` 批量注册：内部按 `DependsOn` 拓扑排序后逐个 `Register`，声明顺序自由
   - `Resolve()` 时仍做环检测
3. **环检测错误**：`ErrPluginCycle` 哨兵 + `CycleError{Cycle: []PluginID}` typed error；调用方用 `errors.Is` / `errors.As` 拿通用判断和详细路径
4. **permission 入口**：`Call` 第一步根据 `plugin.Read()` 路由到 `c.CheckRead(ctx, id)` / `c.CheckWrite(ctx, id)`（round 2 决议）
5. **事件 topic 区分**（round 3 决议，与 round 1 区别）：
   - **LLM 看到的工具名 = `<id>`**（不带 `plugin.` 前缀；`store.localfile.write`）
   - **引擎事件 topic = `plugin.<id>.<lifecycle>`**（**带 `plugin.` 前缀**；`plugin.store.localfile.write.start`）
   - 理由：事件是 engine 内部广播，需要 `plugin.` 前缀避免与用户自定义 topic 冲突；LLM 工具名要简洁
6. **builtin/util.echo**：v5 起步内置 1 个最小 plugin `util.echo`（返 args 原样），供 `tools` 包自带 e2e 测试用；其他 plugin 用户自己写
7. **依赖方向避免循环**：tools 不直接 import ndrive，而是定义 `tools.Context` 最小接口（起步只有 `Bus() bus.Bus`）；`ndrive.Workspace` 实现该接口，`NewToolsAPI(ws)` 时塞入

**外部 import**：`github.com/wyx0k/ndrive/tools`

**walkthrough 决议记录**：
- ✅ Forward ref：方案 c（`RegisterAll` 批量，按 DependsOn 排序）
- ✅ 环检测错误：方案 c（`ErrPluginCycle` 哨兵 + `CycleError` typed error）
- ✅ 事件 topic：`plugin.<id>.<lifecycle>` 带前缀；LLM 工具名 `<id>` 不带前缀
- ✅ builtin 起步：只 `util.echo`

---

### 5.4 `ndrive`（根包 = 容器层）

**职责**：把 `bus` / `permission` / `tools` / id 工厂 / meta 持久化组装起来的容器。**不是薄壳，是真正实现 `Workspace` 的地方** —— `ndrive.Workspace` 类型的定义、`OpenWorkspace` 函数、`MetaConfig` / `SqliteConfig` / `MysqlConfig` 等都直接放在根包的 `.go` 文件里。

**为什么不做薄壳**：
- v4 根包本来就有 `Workspace` / `OpenWorkspace` / `Config` 等大量类型；v5 把它们留在根包、不另设 `workspace/` 子包，路径最短、用户最少改动
- "根包做 re-export 转发子包"会让用户读代码时跳来跳去；"根包本身就是容器"语义最直白
- 子能力（bus / permission / tools）才需要独立顶层包，因为它们可被 workspace 内部使用、也可能被高级用户直接 import

**源文件拆分**（walkthrough round 4 已定，Go 惯例同包多文件）：

```
ndrive.go            # package ndrive 声明 + 公共 import 块（无实现）
workspace.go         # Workspace struct + ID/UserID/Close/Bus/API/Context/Register
config.go            # WorkspaceConfig + MetaConfig + SqliteConfig + MysqlConfig
policy.go            # SetPolicy / GetPolicy / ListPolicies / DeletePolicy
open.go              # OpenWorkspace + OpenWorkspaceConfig 校验
internal/
├── id/              # snowflake 节点
│   └── id.go
└── meta/            # GORM sqlite/mysql 持久化
    └── meta.go
```

**公共 API**：

```go
package ndrive

import (
    "github.com/wyx0k/ndrive/bus"
    "github.com/wyx0k/ndrive/permission"
    "github.com/wyx0k/ndrive/tools"
)

type Workspace struct { /* ... */ }

type WorkspaceConfig struct {
    WorkerID  int64
    Meta      MetaConfig      // SqliteConfig / MysqlConfig 二选一
    BusBuffer int             // v5: 实际生效
}

type MetaConfig interface{ isMetaConfig() }
type SqliteConfig struct { Path string; MaxOpenConn int }
type MysqlConfig struct { Host string; Port int; User, Password, Database string; MaxOpenConns, MaxIdleConns int }

func OpenWorkspace(ctx context.Context, wsID, userID string, cfg WorkspaceConfig) (*Workspace, error)

func (w *Workspace) ID() string
func (w *Workspace) UserID() string
func (w *Workspace) Close() error
func (w *Workspace) Bus() bus.Bus
func (w *Workspace) API() *tools.ToolsAPI
func (w *Workspace) Context(ctx context.Context) context.Context
func (w *Workspace) Register(def tools.SimplePluginDef) error
func (w *Workspace) SetPolicy(ctx context.Context, userID string, mode permission.Mode) error
func (w *Workspace) GetPolicy(ctx context.Context, userID string) (permission.Mode, error)
func (w *Workspace) ListPolicies(ctx context.Context) ([]permission.Binding, error)
func (w *Workspace) DeletePolicy(ctx context.Context, userID string) error
// 注意：v5 不再有 CreateBizmodel / GetBizmodel / ListBizModels / DeleteBizmodel；
// "Bizmodel" 作为概念在 v5 中彻底移除
```

**internal 子包**（根包内，外部不可 import）：
- `ndrive/internal/id` —— snowflake 节点；`Init(workerID)` 严格（workerID==0 自动、二次 Init panic、非 0 冲突检测）
- `ndrive/internal/meta` —— `Store` 接口 + sqlite/mysql 实现；存 workspace / permission_binding / plugin 3 类 key

**v5 关键点**（walkthrough round 4 已定）：

1. **根包**就是**容器层**，**没有 re-export、没有 type alias、没有转发函数** —— 所有类型直接定义、所有方法直接实现
2. **Bizmodel 整条移除**（v4 是核心数据层，v5 不再有此概念）—— 用户的所有能力注册都直接 `ws.Register(tools.SimplePluginDef{...})`，不需要先创建 Bizmodel 容器
3. **meta 存储 3 类 key**：
   - `workspace/<id>` —— workspace 配置 / 元信息
   - `permission_binding/<wsID>/<userID>` —— permission 绑定
   - `plugin/<id>` —— plugin 描述符（启动时回放）
4. **id 工厂**是根包内部依赖；外部不感知 ID 怎么生成
5. **builtin plugin 不自动注册** —— `util.echo` 在 `tools/builtin` 包内提供，**用户需显式调 `ws.API().RegisterBuiltin(builtin.Echo())`** 才会进注册表（opt-in 风格）
6. **workerID 冲突检测**（方案 c）：
   - `cfg.WorkerID == 0` —— 合法值，snowflake 用进程内默认（单进程场景）
   - `cfg.WorkerID != 0` —— 进程级全局检测：同一进程内 2 个 ws 用相同 workerID → `OpenWorkspace` 返 error
   - 二次 `Init` 同 workerID 也 panic
7. **meta store 失败 fail-fast**（方案 a）—— `OpenWorkspace` 打开 sqlite/mysql 失败、schema 不兼容等立即返 error，**不构造半残 ws**
8. **源文件拆分** —— `workspace.go` / `config.go` / `policy.go` / `open.go` / `ndrive.go`（每个 < 200 行）
9. v4 的 `ntd.Config` 类型彻底删除；用户用 `ndrive.WorkspaceConfig`

**外部 import**：
- `github.com/wyx0k/ndrive` —— 容器层（`Workspace` / `OpenWorkspace` / `MetaConfig`）
- `github.com/wyx0k/ndrive/bus` —— 进程内 bus
- `github.com/wyx0k/ndrive/permission` —— 4 档 ACL
- `github.com/wyx0k/ndrive/tools` —— 插件注册中心（`SimplePluginDef` / `PluginID`）
- `github.com/wyx0k/ndrive/tools/builtin` —— 内置 plugin（opt-in 注册）

**walkthrough 决议记录**：
- ✅ 源文件拆分：方案 b（4 个 .go 文件 + ndrive.go 入口）
- ✅ builtin 注册时机：方案 b（opt-in，用户显式 `RegisterBuiltin`）
- ✅ workerID 冲突检测：方案 c（0=自动，非 0 严格）
- ✅ meta 失败行为：方案 a（fail-fast 返 error）

---

## 6. 核心抽象

### 6.1 `PluginID`

```go
type PluginID = string  // "xx.xx.xx..." 点分形式，>=2 段
```

**命名空间约定**（建议，不强制）：
- `store.*` —— 存储类（如 `store.localfile.read` / `store.userdb.find`）
- `func.*` —— 复合功能（如 `func.ppt.read` / `func.note.create`）
- `util.*` —— 工具（如 `util.uuid` / `util.now`）
- 自定义（如 `user.myapp.search` / `user.draft.send`）

**不带 `plugin.` 前缀** —— v4 历史曾有 `plugin.<id>` 形式，v5 明确移除；id 本身就是 plugin 的身份，前缀冗余。

### 6.2 `Plugin` 接口

```go
type Plugin interface {
    ID() PluginID
    Description() string
    Schema() map[string]any         // JSON Schema
    DependsOn() []PluginID         // 依赖的其他 plugin
    Read() bool                     // true = 读，不触发 permission 询问
    Call(ctx context.Context, ws Context, args any) (any, error)
}
```

- `Read() bool` 必须显式声明，permission 据此走读/写分支
- `Schema()` 返回的 JSON Schema 同时用于 LLM tool 生成（Tool 视图）和 UI 渲染（Func 视图）

### 6.3 `SimplePluginDef` helper struct

```go
type SimplePluginDef struct {
    ID_         PluginID
    Description_ string
    Schema_     map[string]any
    DependsOn_  []PluginID
    Read_       bool
    Fn_         func(ctx context.Context, ws Context, args any) (any, error)
}
```

- 5 个 getter（`ID()` / `Description()` / `Schema()` / `DependsOn()` / `Read()`）自动实现 `Plugin` 接口
- `Call()` 直接转发到 `Fn_`
- 字段后缀 `_` 是 Go 惯例：避免与 getter 方法同名；用户写 `tools.SimplePluginDef{ID_: "..."}` 即可
- 大多数场景用 `SimplePluginDef` 足够；需要复杂行为（如动态 `Schema`）时直接实现 `Plugin` 接口

### 6.4 `ToolsAPI`

```go
type ToolsAPI struct { /* ... */ }

func NewToolsAPI(ws Context) *ToolsAPI
func (api *ToolsAPI) Register(plugin Plugin) error            // fail-fast: 重名 / 依赖未注册
func (api *ToolsAPI) Resolve() ([]PluginID, error)           // 拓扑排序 + 环检测
func (api *ToolsAPI) Call(ctx context.Context, id PluginID, args any) (any, error)
func (api *ToolsAPI) Tools() []Tool                          // 给 LLM
func (api *ToolsAPI) Funcs() []Func                          // 给 UI
```

**行为约定**：

- `Register` —— 立即校验 `DependsOn` 已注册，否则 `error`（fail-fast，不支持 forward ref）
- `Resolve` —— DFS 拓扑排序，环检测失败返 error；返回所有 plugin 的稳定执行顺序
- `Call` —— 4 步：permission 门控 → 发 `<id>.start` → 执行 `plugin.Call` → 发 `<id>.done` 或 `<id>.error`
- `Tools()` / `Funcs()` —— 从同一注册表派生两个视图

### 6.5 `Tool` / `Func` 投影

```go
type Tool struct {
    ID          PluginID
    Description string
    Schema      map[string]any
}
type Func struct {
    ID   PluginID
    Call func(ctx context.Context, args any) (any, error)  // bound to ws
}
```

- `Tool` 暴露给 LLM（`OpenAI` / `Anthropic` 风格）：`ID` + `Description` + `Schema`
- `Func` 暴露给 UI（按钮 / 表单）：`ID` + 已 bind `ws` 的 `Call` 函数
- 两个视图从**同一注册表**派生，零额外注册开销

### 6.6 `Workspace`（容器）

定义在 `ndrive` 根包。详见 §5.4。

### 6.7 ~~Bizmodel~~（v5 移除）

v4 的 `Bizmodel` 概念在 v5 中**彻底移除**：
- 没有 `Bizmodel` 类型
- 没有 `CreateBizmodel` / `GetBizmodel` / `ListBizModels` / `DeleteBizmodel` 方法
- 没有 `bizmodel/<wsID>/<id>` 的 meta key

v4 的 Bizmodel 在 v5 中由 plugin 注册表替代 —— 用户的每个能力都直接 `ws.Register(tools.SimplePluginDef{...})`，不再需要先创建容器。

### 6.8 `MetaConfig` + `SqliteConfig` + `MysqlConfig`

定义在 `ndrive` 根包。详见 §5.4。`MetaConfig` 是 `isMetaConfig()` 标记接口，只能用 `SqliteConfig` 或 `MysqlConfig` 实现，强制用户二选一。

### 6.9 `permission.Mode` + 决策矩阵

```go
const (
    RAsk Mode = iota
    R
    WAsk
    W
)
```

| mode | read==true | read==false |
|---|:-:|:-:|
| RAsk | ✅ 直接放行 | ❓ 发 `permission.ask` |
| R | ✅ 直接放行 | ❌ 拒绝 |
| WAsk | ❓ 发 `permission.ask` | ❓ 发 `permission.ask` |
| W | ❌ 拒绝 | ✅ 直接放行 |

### 6.10 引擎事件约定

**引擎自动发**（在 `Call` 前后）：
- `plugin.<id>.start` —— `Call` 入口
- `plugin.<id>.done` —— `Call` 成功
- `plugin.<id>.error` —— `Call` 失败

> **带 `plugin.` 前缀** —— 与 LLM 工具名（`<id>` 不带前缀）区分；事件是 engine 内部广播，加 `plugin.` 前缀避免与用户自定义 topic 冲突。

**permission 询问**（permission 包发）：
- `permission.ask` —— RAsk / WAsk 模式发到 bus，UI 端 `Handle` 后返 `AskResponse`

**plugin 内部自定义**：
- `file.changed` / `user.draft.sent` / ... —— plugin 在 `Fn_` 内部可用 `ws.Bus().Publish(...)` 发任何 topic（不带 `plugin.` 前缀，因为不是引擎事件）

---

## 7. 关键决策表（v4 → v5 修订）

**新增**：

- 28: 3 个顶层子包 + 1 个根包容器组织（bus / permission / tools + 根 ndrive 容器层）
- 29: id 降为 `ndrive/internal`（不暴露）
- 30: tree 概念整体移除（Node / Brief / Tree / Watchable 全部删除）
- 31: 5 个 backend 全部移除（v5 起步不内置任何 store plugin）
- 32: **Plugin** 统一 plugin 机制（v4 Op + Procedure 双轨合为单一接口）
- 33: `SimplePluginDef` helper struct（替代 v4 的 `Cap`）
- 34: `ToolsAPI` 注册中心（Register/Resolve/Call/Tools/Funcs）
- 35: `PluginID` 全局唯一 + 命名空间约定（不带 `plugin.` 前缀）
- 36: `DependsOn` 依赖声明 + `RegisterAll` 批量 + 拓扑排序 + 环检测（环返 `CycleError`）
- 37: Tool/Func 双视图自动派生
- 38: `permission.Checker` 拆成 `CheckRead` / `CheckWrite`（plugin 声明 `Read() bool`，引擎路由）
- 38a: `builtin/util.echo` 起步内置 1 个测试 plugin
- 39: Bizmodel 整条移除（v4 是数据层核心，v5 完全删除）
- 40: 根包改名为 `ndrive`（v4 是 `ntd`）
- 41: 根包作为容器层（v4 根包拆分为 3 顶层子包 + 根包容器；v5 根包不是薄壳而是真实实现 Workspace）
- 42: 机制命名统一为 plugin（v4 是 ext/Op/Procedure 混用）
- 43: 引擎事件 topic **保留** `plugin.` 前缀（`plugin.<id>.<lifecycle>`），仅 LLM 工具名（`<id>`）不带前缀；前者是 engine 内部广播避免与用户 topic 冲突，后者要简洁

**修订**：

- 1: 引擎定位 —— 从"树形业务存储引擎"改为"插件注册框架"
- 2: 树 —— 删除整条决策
- 3: 5 种 bizmodel_type —— 删除（v5 plugin 类型不限）
- 9-11: backend 接口 —— 删除
- 13/13a: meta kind 目录 —— 改为 `workspace` / `permission_binding` / `plugin` 3 类（**bizmodel 移除**）
- 19-23: permission 相关 —— 全部从 `policy` 改名为 `permission`；语义不变
- 24: bus 范围 —— 保留
- 25-26: tree 缓存/冲突检测 —— 删除（无 tree）
- 27: 依赖方向 —— 加强到包级别

**删除**：

- 决策 2、3（tree / 5 种 bizmodel_type）
- 决策 4（bizmodel 整条 —— v5 完全移除，结构 CRUD 不再存在）
- 决策 9-12（backend 全部）
- 决策 25-26（tree 缓存/冲突）

---

## 8. 数据流

### 8.1 调用一个 plugin

```
用户: ws.API().Call(ctx, "user.myapp.search", map[string]any{"q": "hello"})
  ↓
ToolsAPI.Call:
  1. permission 门控：按 plugin.Read() 路由 → c.CheckRead(ctx, "user.myapp.search") 或 c.CheckWrite(ctx, "user.myapp.search")
  2. bus.Publish("plugin.user.myapp.search.start", {...})        ← 引擎事件带 plugin. 前缀
  3. plugin.Call(ctx, ws, args)
  4. bus.Publish("plugin.user.myapp.search.done", {result}) | 失败: "plugin.user.myapp.search.error"
  5. return result
```

### 8.2 plugin 内调别的 plugin

```go
ws.API().Call(ctx, "store.userdb.find", map[string]any{"id": 42})
```
（在 `SimplePluginDef.Fn_` 内部；`ws.API().Call` 内部仍会过 permission 门控 + 发事件）

### 8.3 注册 plugin

```go
import (
    "github.com/wyx0k/ndrive"        // Workspace, OpenWorkspace
    "github.com/wyx0k/ndrive/tools"  // SimplePluginDef, PluginID
)

ws.API().Register(tools.SimplePluginDef{
    ID_:          "func.ppt.read",
    Description_: "读取 pptx 并返回文本",
    Schema_:      {"type": "object", "properties": {"path": {"type": "string"}}},
    DependsOn_:   []tools.PluginID{"store.userdb.read"},
    Read_:        false,  // 写
    Fn_: func(ctx, ws, args) (any, error) {
        // ... 用 ws.Bus() 发事件，用 ws 的 internal meta 存临时数据
    },
})
```

### 8.4 meta 持久化 key 约定

| key 模式 | 含义 |
|---|---|
| `workspace/<id>` | workspace 配置 / 元信息 |
| `permission_binding/<wsID>/<userID>` | permission 绑定 |
| `plugin/<id>` | plugin 描述符（启动时回放） |

v4 还有 `bizmodel/<wsID>/<id>` —— **v5 移除**，因为 Bizmodel 整个删除。

---

## 9. Use Case

### 9.1 旧 v4 场景的 v5 重写

保留"用户用本地文件存笔记"的等价路径：

**v4 步骤**：
```go
bm.Call(ctx, "file-fm-write", "/notes/a.md", map[string]any{...})
bm.Tree().ListWithBrief(ctx, "/notes")
```

**v5 等价**（**用户自己写 plugin**，因为 v5 不内置 file backend）：

```go
import (
    "github.com/wyx0k/ndrive"        // Workspace, OpenWorkspace
    "github.com/wyx0k/ndrive/tools"  // SimplePluginDef, PluginID
)

// 1. 用户先写一个本地文件 plugin
ws.API().Register(tools.SimplePluginDef{
    ID_:          "store.localfile.write",
    Description_: "写入文件",
    Schema_:      {...},
    Read_:        false,
    Fn_: func(ctx, ws, args) (any, error) {
        a := args.(map[string]any)
        os.WriteFile(a["path"].(string), []byte(a["body"].(string)), 0644)
        ws.Bus().Publish("file.changed", map[string]any{"path": a["path"]})
        return nil, nil
    },
})

// 2. 调用
userCtx := ws.Context(ctx)
ws.API().Call(userCtx, "store.localfile.write", map[string]any{
    "path": "/notes/a.md", "body": "...",
})
```

**说明 v5 哲学**：v4 把"本地文件"作为内置能力；v5 让"本地文件"成为一个普通 plugin，引擎不关心 plugin 操作什么（文件 / 数据库 / HTTP / 内存都行）。

### 9.2 permission WAsk 询问流程

```
LLM: ws.API().Call(ctx, "user.draft.send", {to: "alice", body: "..."})
  ↓
ToolsAPI.Call:
  1. permission.Check(ctx, "user.draft.send", read=false)
     mode = WAsk, write=true → c.ask(ctx, ...)
  2. bus.Request("permission.ask", AskPayload{Op: "user.draft.send", Preview: "..."})
  3. UI 端 Handle("permission.ask") → 用户点确认 → AskResponse{Approved: true}
  4. plugin.Call(...) → 发 bus "plugin.user.draft.send.done"（**加 plugin. 前缀**，是引擎事件）
```

### 9.3 跨 plugin 依赖

```go
ws.API().Register(tools.SimplePluginDef{
    ID_: "func.ppt.read", DependsOn_: []tools.PluginID{"store.localfile.read"},
    Fn_: func(ctx, ws, args) (any, error) {
        raw, _ := ws.API().Call(ctx, "store.localfile.read", map[string]any{"path": args.(map[string]any)["path"]})
        return parsePPTX(raw.([]byte))
    },
})
// Resolve 返回 ["store.localfile.read", "func.ppt.read"]
```

---

## 10. 关键设计模式

**v4 保留**：
- 单例 Workspace、create-or-open、userID 一致性保护、强类型 Config、typed errors、Per-workspace 隔离

**v4 删除**：
- 封闭 Backend（v5 任何 plugin 都能注册，包括 store 类）
- Tree 缓存、Tree 冲突检测
- ProcedureEvent 单独事件（v5 用 `<id>.start/done/error` 通用）

**v5 新增**：
- **Plugin 统一机制**（核心）
- **Register/Resolve/Call 三段式**
- **Tool/Func 自动派生投影**
- **依赖声明 + 拓扑排序**
- **3 个顶层子包 + 根包容器分层**
- **`tools.Context` 接口**（避免 tools ↔ ndrive 循环）

---

## 11. Workspace 生命周期

| 阶段 | 行为 |
|---|---|
| `OpenWorkspace` 入口 | 1. 初始化 `internal/id`（workerID 冲突检测）<br>2. 打开 `internal/meta`（sqlite/mysql GORM）<br>3. 构造 `bus.Bus`（`BusBuffer` 生效）<br>4. 构造 `permission.Checker`（注入 meta store + bus）<br>5. 构造 `tools.ToolsAPI`（注入 `Context` = self）<br>6. 加载 `plugin/<id>` 描述符回放（如果之前注册过） |
| `ws.Register(def)` | 调 `ToolsAPI.Register`；`DependsOn` 未注册则 fail-fast |
| `ws.API().Call(...)` | permission 门控 → 发 `<id>.start` → 执行 plugin → 发 `<id>.done/error` |
| `ws.Close()` | 关闭 `internal/meta`；`internal/id` 无状态无需关闭；`bus` 关闭（v5 仍单进程） |

**新增点（v5 相对 v4）**：
- `OpenWorkspace` 末尾自动 `api.Resolve()`（如果用户没注册任何 plugin 也不报错）
- 启动时回放 `plugin/<id>` 描述符（meta 持久化的 plugin 描述符可以 rehydrate）

---

## 12. 迁移路径（hard break）

| v4 | v5 |
|---|---|
| `import "github.com/wyx0k/ndrive"` | `import "github.com/wyx0k/ndrive"`（容器层，直接）；或精确 import `bus` / `permission` / `tools` |
| `ntd.Config` / `ntd.WorkspaceConfig` | `ndrive.WorkspaceConfig`（根包直接定义）|
| `ntd.OpenWorkspace(...)` | `ndrive.OpenWorkspace(...)`（同一函数，根包实现）|
| `bm.Call(opName, path, args)` | `ws.API().Call(ctx, pluginID, args)` |
| `bm.Tree().ListWithBrief(...)` | **删除**（v5 无 tree）；用户用 plugin 自己实现 list |
| `bm.RegisterProcedure(p)` | `ws.API().Register(tools.SimplePluginDef{...})` |
| `bm.CallProcedure(name, args)` | `ws.API().Call(ctx, name, args)` |
| `bm.Procedures()` | `ws.API().Tools()` / `ws.API().Funcs()` |
| `ws.CreateBizmodel(...)` / `GetBizmodel` / `ListBizModels` / `DeleteBizmodel` | **全部删除**（v5 无 Bizmodel，直接 `ws.Register(def)`）|
| `ntd.Procedure` / `ntd.ProcedureEvent` / `ntd.BizModelType` / `ntd.SubKind` / `ntd.NamedOp` / `ntd.Op` / `ntd.OpArgs` / `ntd.BizmodelResolver` / `ntd.Bizmodel` | **全部删除** |
| `ntd.Node` / `ntd.Brief` / `ntd.Tree` | **全部删除**（v5 无 tree）|
| `ntd.BackendLocalFile` 等 5 个常量 | **全部删除**（v5 不内置 backend）|
| `ntd.RegisterBackend(p)` | **删除**（v5 用户直接 `ws.API().Register(def)`）|
| `pkgid.Init` / `Next`（v4 公开）| **降为 `ndrive/internal/id`**（用户不感知）|
| `pkg/bus.ErrRequestTimeout` 永不返回 | **真的实现**（v5 Request 监听 ctx）|
| 5 个 default backend 包 | **全部删除** |
| `pkg/subkind` 整个包 | **删除** |
| `pkg/tree` 整个包 | **删除** |
| `pkg/meta` 公开包 | **降为 `ndrive/internal/meta`** |
| `pkg/ctx` 公开包 | **改为各包内私有**（permission 的 ctx key 在 permission 包内，ndrive 的 ctx key 在 ndrive 包内）|
| `ExtAPI` / `ExtID` / `ext.<id>.*` 事件 | **改名为** `Plugin` / `PluginID` / `<id>.*` 事件 topic（**去掉 `plugin.` 前缀**）|
| `tools.Cap` helper struct | **改名为** `tools.SimplePluginDef` |
| `ntd.OwnerID` 字段 / `owner_id` 概念 | **改名为** `ndrive.UserID` / `user_id`（全仓库统一术语）|
| `policy.ask` 事件 topic | **改名为** `permission.ask` |
| `policy.Check(op)` 单方法 | **拆成** `permission.CheckRead(op)` / `permission.CheckWrite(op)`（按 plugin.Read() 路由）|

---

## 13. 待决 / 已知风险

- ~~**`ndrive` 根包作为容器层是否过重**~~ —— **已决议**（round 4）：拆 4 个 .go 文件（`workspace.go` / `config.go` / `policy.go` / `open.go`），每个 < 200 行
- **walkthrough 阶段才确定的细节**：
  - ~~`permission.Checker.Check` 的 `read bool` 参数命名 / 位置~~ —— **已决议**（round 2）：方案 B，拆成 `CheckRead` / `CheckWrite`
  - ~~`tools.ToolsAPI.Register` 的 forward ref 策略~~ —— **已决议**（round 3）：方案 c，`RegisterAll` 批量注册 + 内部按 DependsOn 排序
  - ~~`bus.Request` ctx 超时默认 deadline~~ —— **已决议**（round 1）：方案 B，handler 内部 listen ctx，Request 不开 goroutine 不设默认 deadline
  - ~~根包 `internal/id` 的 workerID 冲突检测~~ —— **已决议**（round 4）：方案 c，0=自动 / 非 0 严格
  - ~~meta store 失败行为~~ —— **已决议**（round 4）：方案 a，fail-fast 返 error
- **完全删除 tree 的代价**：用户用 v5 时要自己写 file/db/list 等所有 plugin；如果用户没有可复用的 plugin 库，v5 反而比 v4 难上手
- ~~**`tools/builtin` 是否真的不需要**~~ —— **已决议**（round 3）：只内置 `util.echo`（最简测试用）
- **根包权限边界**：用户用 v5 时需要同时 import `ndrive`（拿 Workspace）和 `tools`（拿 SimplePluginDef / Plugin）—— 比 v4 一个根包多一个 import；确认这是否可接受
- **builtin opt-in 体验**：`util.echo` 不自动注册，用户必须写一行 `ws.API().RegisterBuiltin(builtin.Echo())`；v5 release 时考虑加 e2e 示例覆盖这一点

---

## 14. 落地顺序（未来重构 checklist，本次文档完成）

3 个顶层子包 + 1 个根包容器逐步实施：

1. **`bus`** —— v4 主体保留 + 3 处修订（ctx timeout / Handle error / 去掉 buffer 参数）
2. **`permission`** —— v4 `policy` 重命名 + `Checker` 加 `read` 参数 + `NewChecker` 注入 store
3. **`tools`** —— 全新写（`Plugin` / `SimplePluginDef` / `ToolsAPI` / `Context` 接口）
4. **`ndrive` 根包（容器）** —— 从 v4 根包瘦身 + 引入 `internal/{id,meta}` + 接入 tools/permission/bus + 删除 Bizmodel

每步标"涉及哪个包"，给出 v4 → v5 的代码迁移映射。详细迁移见 §12。
