# NDrive v5 实施 Specs

> 本文档列出 v5 架构的实施分解。每个 spec 是一次独立 PR 级别的实现单位。
> 设计参考：[ARCHITECTURE.md](ARCHITECTURE.md)（v5）

## Spec 列表

| # | 名称 | 依赖 | 关键文件 | 状态 |
|---|---|---|---|---|
| 1 | bus package | - | `bus/bus.go`, `bus/bus_test.go` | ✅ |
| 2 | permission package | Spec 1 | `permission/permission.go`, `permission/permission_test.go` | ✅ |
| 3 | tools - core types | Spec 1 | `tools/tools.go`, `tools/types_test.go` | ✅ |
| 4 | tools - registry | Spec 3 | `tools/registry.go`, `tools/registry_test.go` | ✅ |
| 5 | tools - dispatch | Spec 2, 4 | `tools/dispatch.go`, `tools/dispatch_test.go` | ✅ |
| 6 | tools/builtin | Spec 3 | `tools/builtin/builtin.go`, `tools/builtin/builtin_test.go` | ✅ |
| 7 | ndrive - core + config | Spec 1, 4 | `ndrive.go`, `workspace.go`, `config.go`, `open.go` + tests | ✅ |
| 8 | ndrive - permission methods | Spec 2, 7 | `policy.go`, `policy_test.go` | ✅ |
| 9 | ndrive/internal/id | Spec 7 | `internal/id/id.go`, `internal/id/id_test.go` | ✅ |
| 10 | ndrive/internal/meta | Spec 7 | `internal/meta/meta.go`, `internal/meta/meta_test.go` | ✅ |
| 11 | cleanup v4 remnants | All | 删 `pkg/tree`, `pkg/backend`, `pkg/subkind`, `pkg/ctx`, `cmd/ntd`；更新 tests | ✅ |
| 12 | tools - Plugin/Tool 二层拆分 | Spec 3, 4, 5 | `tools/tools.go`, `tools/registry.go`, `tools/dispatch.go`, `plugins/localfs/plugin.go` + tests | ✅ |
| 13 | localfs - 写能力扩展 | Spec 12 | `plugins/localfs/localfs.go`, `plugins/localfs/plugin.go`, `tools/builtin/service.go` + tests | ✅ |

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
- 定义两层接口：
  - `Plugin` interface（容器）：`ID() PluginID` / `Tools() []Tool`
  - `Tool` interface（可调用单元）：`ID() string` / `Description()` / `Schema()` / `DependsOn() []PluginID` / `Read() bool` / `Internal() bool` / `Call(ctx, ws, args) (any, error)`
- `SimplePluginDef` struct（7 field：`ID_/Description_/Schema_/DependsOn_/Read_/Internal_/Fn_`）同时实现 `Tool` 和 `Plugin`（单 tool 容器）
- `AsPlugin(t Tool) Plugin` —— 把裸 `Tool` 包成单 tool 容器
- `Context` interface, `ToolInfo` / `Func` 投影结构
- 暂不实现 `ToolsAPI` / `Register` / `Call`（Spec 4 + 5）

### Spec 4: tools - registry
- `ToolsAPI` struct（`tools` 全 id 索引 + `containers` 容器 id 索引 + `lifecycleTargets` 容器 id 索引）+ `NewToolsAPI(ws Context) *ToolsAPI`
- `Register(v any, cfg any) error` —— `v` 可以是容器 `Plugin` 或裸 `Tool`（自动 `AsPlugin` 包装）；立即校验容器 id + 每个 tool 的 full id，校验 `DependsOn` 已注册；若 lifecycle target 实现 `StatefulPlugin` 则调 `StartPlugin(ws, cfg)`；失败整体回滚，不留半成品
- `RegisterAll(plugins []Plugin) error` —— 按 `DependsOn` 拓扑排序，按容器去重后依次 `StartPlugin`；中途失败按已启动顺序反向 `StopPlugin` 回滚
- `Resolve() ([]PluginID, error)` —— 对 full id 索引做 DFS 拓扑排序 + 环检测
- 可选容器能力接口：`StatefulPlugin`（`StartPlugin/StopPlugin`）、`ReloadablePlugin`（`UpdateConfig`）、`ServicePlugin`（`Service`）
- 错误：`ErrPluginNotFound` / `ErrPluginDuplicate` / `ErrPluginCycle` + `CycleError{Cycle: []PluginID}` typed error
- 暂不实现 `Call`（Spec 5）

### Spec 5: tools - dispatch
- `Call(ctx, id, args) (any, error)` —— 按 full id 查 `tools`，4 步：
  1. permission 门控（按 `tool.Read()` 路由 `CheckRead` / `CheckWrite`）
  2. 发 `plugin.<full-id>.start` 事件
  3. 调 `tool.Call`
  4. 发 `plugin.<full-id>.done` 或 `plugin.<full-id>.error`
- `Tools() []ToolInfo` —— LLM 视角投影，过滤 `Internal()==true`
- `Funcs() []Func` —— UI 视角投影（Call 已 bind ws），同样过滤 `Internal()==true`
- `UpdatePluginConfig(id, cfg) error` / `Service(id) (any, error)` —— 按容器 id 查 `lifecycleTargets`
- `StopAllPlugins() error` —— 对所有 lifecycle target 调 `StopPlugin`，`errors.Join` 汇总错误，单个失败不中断其余

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

### Spec 12: tools - Plugin/Tool 二层拆分
- 动机：Spec 3-5 的单层 `Plugin` 把"容器"和"可调用单元"耦合成一个接口，无法表达一个容器下挂多个 tool（如本地文件系统下的 `list`/`read`/`search`/`stat`/`open`），也无法表达"内部 tool"（如 `open`：需要被其它 tool 通过 `DependsOn` 间接依赖，但不该直接暴露给 LLM/UI）
- 拆分：
  - `Plugin` 收窄为容器接口：`ID() PluginID` + `Tools() []Tool`
  - 新增 `Tool` 接口承担原 `Plugin` 的可调用职责，新增 `Internal() bool`（默认 `false`）：`Internal()==true` 的 tool 从 `Tools()`/`Funcs()` 投影中隐藏，但仍可通过 `ToolsAPI.Call(ctx, fullID, args)` 正常调用 —— 是"可见性"开关，不是"可调用性"开关
  - full id 规则：`fullToolID(containerID, toolID)`，`toolID` 为空则 full id = containerID，否则 `containerID + "." + toolID`
  - `SimplePluginDef` 扩展到 7 field，同时实现 `Tool` 和 `Plugin`（单 tool 容器），向后兼容原有裸 tool 注册写法
  - `AsPlugin(t Tool) Plugin` 导出辅助函数，配合 `Register` 内部的裸 `Tool` 自动包装
  - `ToolsAPI` 内部从单一 map 拆成三个索引：`tools`（full id）/ `containers`（容器 id）/ `lifecycleTargets`（容器 id，值为真实容器或原始裸 `Tool`，用于 `StatefulPlugin`/`ReloadablePlugin`/`ServicePlugin` 的类型断言）
  - 原投影结构体 `Tool` 改名为 `ToolInfo`（腾出 `Tool` 这个名字给新接口）
- 新增容器级可选能力接口（每个容器生效一次，不是每个 tool）：`StatefulPlugin`（`StartPlugin(ws, cfg) error` / `StopPlugin() error`）、`ReloadablePlugin`（`UpdateConfig(cfg) error`）、`ServicePlugin`（`Service() any`）
- 修复的 bug：裸 `Tool` 自动包装成 `singleToolPlugin` 后，若原 `Tool` 没实现 `StatefulPlugin` 等能力接口，包装器不能"泄漏"出这些能力（即不能让包装器意外满足接口断言）
- 验证示例：`plugins/localfs` 从 4 个独立单 tool plugin 合并成一个 `store.localfs` 容器，`list`/`read`/`search`/`stat`/`write`/`write-markdown`/`edit` 导出 + `open`/`write-stream` 设为 `Internal_: true`；容器同时实现 `StatefulPlugin`（文件监听生命周期）和 `ServicePlugin`（`Service()` 返回 `*FS`）
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿

### Spec 13: localfs - 写能力扩展
- 动机：Spec 12 的 `plugins/localfs` 只有读能力（list/read/search/stat/open），补齐写能力才能支撑"LLM 直接改文件"的场景
- `builtin.FileService` 接口新增 3 个方法（`list/read/search/stat/open` 之外）：
  - `Write(ctx, path, body []byte) (FileEntry, error)` —— 创建或整体覆盖，父目录不存在自动创建
  - `WriteStream(ctx, path, r io.Reader) (FileEntry, error)` —— 流式写入，供其它 plugin 原生调用（不走 JSON 参数）
  - `Edit(ctx, path, old, new string, expectedModTime time.Time) (FileEntry, error)` —— 乐观并发的字符串替换
- `plugins/localfs` 新增 4 个 tool：
  - `write` —— 对应 `FS.Write`，`Read_: false`
  - `write-stream` —— `Internal_: true`，`args["reader"]` 直接传 Go `io.Reader`（和 `open` 的返回值一样是"逃出 JSON"的 Go 值），只给其它 plugin 通过 `ToolsAPI.Call` 原生调用
  - `write-markdown` —— 对应 `FS.Write`，参数 `withfm`(bool)/`fm`(map)/`body`(string)；`withfm=true` 时用 `gopkg.in/yaml.v3` 把 `fm` 序列化成 `---\n<yaml>---\n` 前缀拼在 `body` 前
  - `edit` —— 对应 `FS.Edit`，参数 `path`/`old`/`new`/`modTime`（RFC3339Nano 字符串）；`old` 必须在文件内容中恰好出现一次，否则返回 `ErrOldNotFound`/`ErrOldNotUnique`；`modTime` 与文件当前修改时间不一致返回 `ErrModTimeMismatch`
- 新依赖：`gopkg.in/yaml.v3`（从 indirect 提升为 direct，用于 `write-markdown` 的 frontmatter 序列化）
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿

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
