# NDrive v5 实施 Specs

> 本文档列出 v5 架构的实施分解。每个 spec 是一次独立 PR 级别的实现单位。
> 设计参考：[ARCHITECTURE.md](ARCHITECTURE.md)（v5）

## Spec 列表

| # | 名称 | 依赖 | 关键文件 | 状态 |
|---|---|---|---|---|
| 1 | bus package | - | `bus/bus.go`, `bus/bus_test.go` | ⬜ |
| 2 | permission package | Spec 1 | `permission/permission.go`, `permission/permission_test.go` | ⬜ |
| 3 | tools - core types | Spec 1 | `tools/tools.go`, `tools/types_test.go` | ⬜ |
| 4 | tools - registry | Spec 3 | `tools/registry.go`, `tools/registry_test.go` | ⬜ |
| 5 | tools - dispatch | Spec 2, 4 | `tools/dispatch.go`, `tools/dispatch_test.go` | ⬜ |
| 6 | tools/builtin | Spec 3 | `tools/builtin/builtin.go`, `tools/builtin/builtin_test.go` | ⬜ |
| 7 | ndrive - core + config | Spec 1, 4 | `ndrive.go`, `workspace.go`, `config.go`, `open.go` + tests | ⬜ |
| 8 | ndrive - permission methods | Spec 2, 7 | `policy.go`, `policy_test.go` | ⬜ |
| 9 | ndrive/internal/id | Spec 7 | `internal/id/id.go`, `internal/id/id_test.go` | ⬜ |
| 10 | ndrive/internal/meta | Spec 7 | `internal/meta/meta.go`, `internal/meta/meta_test.go` | ⬜ |
| 11 | cleanup v4 remnants | All | 删 `pkg/tree`, `pkg/backend`, `pkg/subkind`, `pkg/ctx`, `cmd/ntd`；更新 tests | ⬜ |

## Spec 详细说明

### Spec 1: bus package
- 创建 `bus/` 顶层包（Go 模块惯例：源文件在模块根的 `bus/` 子目录）
- 实现 `Bus` interface + 默认 impl
- v5 修订：
  - `Request(ctx, topic, req)` 走方案 B（不开 goroutine；handler 内部 listen ctx；handler 返 error 或 `ctx.Err() != nil` 时 `Request` 返 `ErrRequestTimeout`）
  - `Handle(topic, handler) (unsub func(), err error)` 重名返 `ErrHandlerExists`
  - `New()` 无参数（hard break；v4 `New(int)` 没了）

### Spec 2: permission package
- 创建 `permission/` 顶层包
- v5 修订：
  - 改名 `policy` → `permission`（全仓库统一）
  - `owner_id` → `user_id`
  - `Checker` 拆成 `CheckRead(ctx, op) error` / `CheckWrite(ctx, op) error`
  - 每 user 一个 mode（`SetPolicy(ctx, userID, mode)`）
  - `AskPayload.Preview` 允许空字符串
  - 无订阅者返 `ErrPermissionDenied`
  - ctx 超时由 `permission.ask` handler 内部 listen，permission 收到 `ErrRequestTimeout` 转译为 `ErrPermissionDenied`
  - `WorkspaceStore` 接口注入给 `NewChecker`（封装 `Set/Get/List/Delete`）
  - 移除 v4 `pkg/policy.Bus` 重复 interface，直接 import `bus` 顶层包

### Spec 3: tools - core types
- 创建 `tools/` 顶层包
- 定义：`PluginID` (string alias), `Plugin` interface, `SimplePluginDef` struct + 5 getters, `Context` interface, `Tool` / `Func` 投影结构
- 暂不实现 `ToolsAPI` / `Register` / `Call`（Spec 4 + 5）

### Spec 4: tools - registry
- `ToolsAPI` struct + `NewToolsAPI(ws Context) *ToolsAPI`
- `Register(plugin Plugin) error` —— 立即校验 `DependsOn` 已注册
- `RegisterAll(plugins []Plugin) error` —— 按 `DependsOn` 拓扑排序后逐个 `Register`
- `Resolve() ([]PluginID, error)` —— DFS 拓扑排序 + 环检测
- 错误：`ErrPluginNotFound` / `ErrPluginDuplicate` / `ErrPluginCycle` + `CycleError{Cycle: []PluginID}` typed error
- 暂不实现 `Call`（Spec 5）

### Spec 5: tools - dispatch
- `Call(ctx, id, args) (any, error)` —— 4 步：
  1. permission 门控（按 `plugin.Read()` 路由 `CheckRead` / `CheckWrite`）
  2. 发 `plugin.<id>.start` 事件
  3. 调 `plugin.Call`
  4. 发 `plugin.<id>.done` 或 `plugin.<id>.error`
- `Tools() []Tool` —— LLM 视角投影
- `Funcs() []Func` —— UI 视角投影（Call 已 bind ws）

### Spec 6: tools/builtin
- 创建 `tools/builtin/` 子包
- `util.echo` plugin：`ID` = "util.echo", `Description` = "返回 args 原样", `Read_` = true, `Fn_` = func 返回 `args`
- Opt-in：用户显式 `ws.API().RegisterBuiltin(builtin.Echo())`

### Spec 7: ndrive - core + config
- **Phase 0**：根包 `package ntd` → `package ndrive`（8 个 .go 文件全改）
- 创建：
  - `ndrive.go` —— `package ndrive` + 公共 import 块（无实现）
  - `workspace.go` —— `Workspace` struct + `ID/UserID/Close/Bus/API/Context/Register` 方法
  - `config.go` —— `WorkspaceConfig` / `MetaConfig` / `SqliteConfig` / `MysqlConfig`
  - `open.go` —— `OpenWorkspace` 函数
- 移除：`Bizmodel` / `CreateBizmodel` / `GetBizmodel` / `ListBizModels` / `DeleteBizmodel`（v5 移除）
- `OpenWorkspace` 流程：构造 bus → 构造 meta store → 构造 internal/id → 构造 tools.ToolsAPI → 构造 permission.Checker → 拼装 Workspace
- 失败行为：meta store 失败 fail-fast 返 error（不构造半残 ws）

### Spec 8: ndrive - permission methods
- `policy.go`：
  - `SetPolicy(ctx, userID, mode) error`
  - `GetPolicy(ctx, userID) (Mode, error)`
  - `ListPolicies(ctx) ([]Binding, error)`
  - `DeletePolicy(ctx, userID) error`
- 通过 `internal/meta` 持久化到 `permission_binding/<wsID>/<userID>` key

### Spec 9: ndrive/internal/id
- 移 `pkg/id/` → `internal/id/`
- `Init(workerID int64)`：
  - `workerID == 0` —— 用进程内自动分配（合法，绕过严格检测）
  - `workerID != 0` —— 进程级全局检测；同 workerID 已 Init 过 → panic
- 移除 v4 silent auto-init
- `Next() (int64, error)` 返回 snowflake ID

### Spec 10: ndrive/internal/meta
- 移 `pkg/meta/` → `internal/meta/`
- `Store` interface + sqlite/mysql GORM 实现
- 3 类 key：
  - `workspace/<id>` —— workspace 配置
  - `permission_binding/<wsID>/<userID>` —— permission 绑定
  - `plugin/<id>` —— plugin 描述符（启动时回放）
- `Store.Put/Get/Delete/List(ctx, key, value) error`

### Spec 11: cleanup v4 remnants
- 删除：
  - `pkg/tree/` 整个包（Node / Brief / Tree / Watchable / Publisher）
  - `pkg/backend/` + 5 子包（localfile / localsqlite / localbadger / remotesql / remotemongo）
  - `pkg/subkind/`
  - `pkg/ctx/`
  - `cmd/ntd/`
- 更新：
  - 所有 `*_test.go` 用 `ndrive.X` 替换 `ntd.X`（已大部分通过 Spec 7 的 Phase 0 解决）
  - `examples/local_file/local_file_example.go` 改写为 v5 plugin 注册示例
  - `docs/` 下文档同步更新（可选，本次先跳过）
- 验证：`go build ./...` + `go test ./...` 全绿

## 实施顺序

按 spec 编号顺序：

```
Spec 1 → Spec 2 ┐
   ↓             ├─→ Spec 5
   → Spec 3 → Spec 4 ┘
                ↓
              Spec 6 (parallel with 5)
                ↓
              Spec 7 ─→ Spec 8
                    ─→ Spec 9
                    ─→ Spec 10
                ↓
              Spec 11 (depends on all)
```

实际可并行：Spec 5 等 Spec 2 + 4；Spec 8/9/10 互相独立可并行；Spec 11 必须在所有之后。
