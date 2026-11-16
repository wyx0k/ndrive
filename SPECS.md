# NDrive v5 实施 Specs

> 本文档列出 v5 架构的实施分解。每个 spec 是一次独立 PR 级别的实现单位。
> 设计参考：[ARCHITECTURE.md](ARCHITECTURE.md)
>
> Spec 1–16 是 v5 的实施历史；**Spec 17 是 v6 架构重构**，它改写了 Spec 3 / 5 / 6 / 9 / 12 / 13 / 15 涉及的接口形状；**Spec 18 是 v6 的第二刀 —— 去掉 Plugin 容器层**，它再次改写了 Spec 3 / 4 / 5 / 12 / 14 涉及的接口形状；**Spec 19 是 v6 的第三刀 —— 配置契约开放化 + 权限语义澄清 + 文件变更上总线**，它改写了 Spec 2 / 3 / 4 / 5 / 17 涉及的接口形状。下面各节的描述以 Spec 17 + Spec 18 + Spec 19 为准。

## Spec 列表

| # | 名称 | 依赖 | 关键文件 | 状态 |
|---|---|---|---|---|
| 1 | bus package | - | `bus/bus.go`, `bus/bus_test.go` | ✅ |
| 2 | permission package | Spec 1 | `permission/permission.go`, `permission/permission_test.go` | ✅ |
| 3 | tools - core types | Spec 1 | `tools/tools.go`, `tools/types_test.go` | ✅ |
| 4 | tools - registry | Spec 3 | `tools/registry.go`, `tools/registry_test.go` | ✅ |
| 5 | tools - dispatch | Spec 2, 4 | `tools/dispatch.go`, `tools/dispatch_test.go` | ✅ |
| 6 | tools/builtin | Spec 3 | `tools/builtin.go`, `tools/builtin_test.go`（Spec 17 起在 `tools` 包内） | ✅ |
| 7 | ndrive - core + config | Spec 1, 4 | `ndrive.go`, `workspace.go`, `config.go` + tests | ✅ |
| 8 | ndrive - permission methods | Spec 2, 7 | `policy.go` | ✅ |
| 9 | ndrive/internal/id | Spec 7 | ~~`id/id.go`~~（Spec 17 整体移除） | ➖ 已移除 |
| 10 | ndrive/internal/meta | Spec 7 | `meta/meta.go`, `meta/workspace.go`, `meta/permission.go`, `meta/keys.go` | ✅ |
| 11 | cleanup v4 remnants | All | 删 `pkg/tree`, `pkg/backend`, `pkg/subkind`, `pkg/ctx`, `cmd/ntd`；更新 tests | ✅ |
| 12 | tools - Plugin/Tool 二层拆分（Spec 18 起整体移除） | Spec 3, 4, 5 | ~~`tools/tools.go`, `tools/registry.go`, `tools/dispatch.go`~~ | ➖ 已移除 |
| 13 | localfs - 写能力扩展 | Spec 12 | `fs/local/local.go`, `fs/fs.go` + tests | ✅ |
| 14 | tools - DependsOn 下放到容器级别（Spec 18 起回到 tool 级） | Spec 12 | `tools/tools.go`, `tools/registry.go`, `tools/registry_test.go`, `tools/types_test.go` + tests | ✅ |
| 15 | localfs - 删除能力 | Spec 13 | `fs/local/local.go`, `fs/fs.go` + tests | ✅ |
| 16 | localfs - 路径安全校验（软链越界） | Spec 13 | `fs/local/local.go`, `fs/local/path_test.go` | ✅ |
| 17 | v6 架构重构：fs-api + 单一入口 | All | `fs/fs.go`, `fs/local/local.go`, `tools/dispatch.go`, `tools/registry.go`, `config.go`, `ndrive.go`, `workspace.go` | ✅ |
| 18 | v6 架构重构：去掉 Plugin 容器层 | All | `tools/tools.go`, `tools/registry.go`, `tools/dispatch.go`, `tools/builtin.go`, `workspace.go` + tests | ✅ |
| 19 | v6 架构重构：配置契约开放化 + 权限语义澄清 + 文件变更上总线 | Spec 17, 18 | `fs/fs.go`, `fs/local/config.go`, `meta/meta.go`, `meta/sqlite/sqlite.go`, `meta/mysql/mysql.go`, `permission/permission.go`, `tools/tools.go`, `tools/registry.go`, `config.go`, `ndrive.go`, `workspace.go`, `fsevents.go` + tests | ✅ |

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
  - `Tool` interface（可调用单元）：`ID() string` / `Description()` / `Schema()` / `DependsOn() []PluginID` / `Read() bool` / `Internal() bool` / `Call(ctx, ws, args) (any, error)`（`DependsOn` 后来在 Spec 14 下放到容器级别，不再是 `Tool` 的方法）
- `SimplePluginDef` struct（7 field：`ID_/Description_/Schema_/DependsOn_/Read_/Internal_/Fn_`）同时实现 `Tool` 和 `Plugin`（单 tool 容器）
- `AsPlugin(t Tool) Plugin` —— 把裸 `Tool` 包成单 tool 容器
- `Context` interface, `ToolInfo` / `Func` 投影结构
- 暂不实现 `ToolsAPI` / `Register` / `Call`（Spec 4 + 5）

### Spec 4: tools - registry
- `ToolsAPI` struct（`tools` 全 id 索引 + `containers` 容器 id 索引 + `lifecycleTargets` 容器 id 索引）+ `NewToolsAPI(ws Context) *ToolsAPI`
- `Register(v any, cfg any) error` —— `v` 可以是容器 `Plugin` 或裸 `Tool`（自动 `AsPlugin` 包装）；立即校验容器 id + 每个 tool 的 full id，校验 `DependsOn` 已注册（Spec 14 后改为容器级别校验）；若 lifecycle target 实现 `StatefulPlugin` 则调 `StartPlugin(ws, cfg)`；失败整体回滚，不留半成品
- `RegisterAll(plugins []Plugin) error` —— 按 `DependsOn` 拓扑排序，按容器去重后依次 `StartPlugin`（Spec 14 后拓扑排序直接按容器粒度，不再需要去重）；中途失败按已启动顺序反向 `StopPlugin` 回滚
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

### Spec 14: tools - DependsOn 下放到容器级别
- 动机：Spec 3 把 `DependsOn` 定义在 `Tool` 接口上，但 Spec 12 拆出 `Plugin`（容器）/`Tool`（可调用单元）两层后，依赖关系在语义上属于容器——一个容器下的多个 tool 共享内部状态，天然应该共享同一份依赖声明；`Tool` 级别的 `DependsOn` 既无法被 `plugins/localfs` 这类多 tool 容器自然表达（没有任何单个 tool 适合代表整个容器的依赖），也和 `StatefulPlugin`/`ReloadablePlugin`/`ServicePlugin` 已经确立的"容器级别可选接口"风格不一致
- 改动：
  - `Tool` 接口移除 `DependsOn() []PluginID`
  - 新增可选接口 `DependentPlugin`（嵌 `Plugin` + `DependsOn() []PluginID`），风格对齐 `StatefulPlugin` 等；未实现该接口的 `Plugin` 视为没有依赖，不是编译期强制
  - `SimplePluginDef` 保留 `DependsOn_` 字段，`DependsOn()` getter 改为只实现 `DependentPlugin`（不再是 `Tool` 的方法）；单 tool 容器场景下容器 id 就是 `ID_`，写法不变
  - `registry.go` 新增 `containerDependsOn(Plugin) []PluginID` helper（类型断言 `DependentPlugin`，未实现返 `nil`）；`Register`/`RegisterAll` 的依赖校验从"每个 tool 的 full id 查 `a.tools`"改为"每个容器查 `a.containers`"
  - 拓扑排序从 `topoSort(map[PluginID]Tool)`（tool full id 粒度）改写成 `topoSortContainers(map[PluginID]Plugin)`（容器 id 粒度）；`RegisterAll` 不再需要 `containerIDOf` 前缀匹配反查容器（直接在容器粒度排序）
  - `Resolve()` 返回值语义变化：从"tool full id 的拓扑序"变成"容器 id 的拓扑序"（多 tool 容器只出现一次，不按 tool 数量重复）
- 向后兼容：所有现有 `SimplePluginDef{DependsOn_: [...]}` 用法不变（单 tool 容器下容器 id == tool full id）；只有手写 `Tool`（不再声明 `DependsOn()`）和手写 `Plugin` 想要依赖声明（需额外实现 `DependentPlugin`）的场景需要跟着接口改动调整
- 新增测试覆盖此前缺失的场景：`multiToolStub`（真正的多 tool 容器）声明容器级别依赖，验证 (a) 缺依赖时 `Register` 整体失败、满足后两个 tool 都能注册且可调用；(b) `Resolve()` 对多 tool 容器只返回一次容器 id，拓扑顺序正确
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿

### Spec 15: localfs - 删除能力
- 动机：Spec 13 补齐了写能力（write/write-markdown/edit），但没有删除——"LLM 整理目录 / 清理临时文件"这类场景做不了，容器里也就没有和 Write/Edit 对称的破坏性操作
- `builtin.FileService` 接口新增 1 个方法：
  - `Delete(ctx, path string, recursive bool) error` —— 删除文件或目录；目录必须显式 `recursive=true` 才递归删除，否则返回 error（避免"删个文件"顺手删掉整棵子树）；删除沙盒根目录本身一律拒绝
- `plugins/localfs` 新增 1 个 tool：
  - `delete` —— 对应 `FS.Delete`，参数 `path`(string，必填)/`recursive`(bool，默认 false)；`Read_: false`（走 `CheckWrite` 权限门控，破坏性操作不能默认放行）
- `plugins/localfs` 新增 2 个哨兵错误：
  - `ErrDeleteRoot` —— 试图删除沙盒根目录本身（空路径 / "." / "./"）
  - `ErrIsDirectory` —— 删目录但没传 `recursive=true`
- 实现细节：
  - 用 `os.Lstat` 而不是 `os.Stat`：指向目录的符号链接按链接本身处理（只删链接，不动目标），避免软链把删除操作放大到沙盒外
  - 路径越界（".." 等）沿用 `resolve` 的 `ErrOutsideRoot` 拒绝
  - 目标不存在返回包装 `os.ErrNotExist` 的 error（不静默成功）
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿

### Spec 16: localfs - 路径安全校验（软链越界）
- 动机：Spec 13 之前，`resolve` 只做**词法**校验（`filepath.Rel` 判断路径是否还在 root 内）。软链/junction 完全不在校验范围内 —— 在 root 里放一个指向 root 之外的链接，`read("link/secret.txt")` 就能把沙盒外的内容读出来，沙盒形同虚设（Windows 上 `mklink /J` 就能造，不需要管理员权限）
- 改动：`FS.resolve` 改成**两层校验**
  - 词法校验：clean 之后必须仍在 root 内（拒绝 `..` 越界写法，行为不变）
  - 链接校验：解开路径里的链接后，真实路径必须仍在 root 内，否则返回 `ErrOutsideRoot`
- 新增内部实现：
  - `evalLinks(abs)` —— 逐段把链接替换成链接目标，直到没有链接为止。**刻意不用 `filepath.EvalSymlinks`**：它在 Windows 上不认 junction（`os.Lstat` 对 junction 不置 `ModeSymlink` 标记，EvalSymlinks 原样返回链接路径）。这里改用 `os.Readlink` 判定：Unix 软链 / Windows 软链 / junction 都能读出目标，普通文件目录读链接会报错，自然被当成"不是链接"
  - `evalLinksOnce(abs)` —— 只解开最靠前的那一个链接，配合 `maxLinkDepth = 40` 防止链接成环时无限展开
  - `realPath(abs)` —— 路径不存在时（写新文件）取"已存在的最深祖先"解链接，剩下的段原样拼上
  - `withinRoot(root, abs)` —— 越界判定，Windows 上先折叠大小写（文件系统默认大小写不敏感）
  - `isLink(path)` —— 链接判定，同样基于 `os.Readlink`
- `FS` 新增 `realRoot` 字段：root 自身可能是链接（macOS 的 `/tmp`、挂载点），越界判定拿真实路径比，否则 root 是链接时所有访问都会被误判成越界
- 内容读取路径单独校验：`readBrief` / `matchesContent` 改成 `FS` 的方法，读取前各自再校验一次 —— 目录条目的中间路径由所在目录的 resolve 校验过，但末位可能是指向 root 之外的链接，读它的内容会把沙盒外的数据带进 list/search 结果
- `Search` 不再进入目录链接：跟着链接递归会把 root 之外的目录树枚举出来（链接本身仍可按名字匹配到，只报条目自身元信息，不读内容）
- 已知残留风险：校验与实际 open 之间存在 TOCTOU 窗口 —— 有本地写权限的攻击者可以在窗口内把路径换成链接。彻底消除需要 `O_NOFOLLOW` 之类的打开语义，跨平台不可得，这里只保证"稳态下不越界"
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿；新增 `plugins/localfs/path_test.go`（Windows 上用 junction 建链接，不需要软链特权；其它平台用软链）

### Spec 17: v6 架构重构 —— fs-api + 单一执行入口

- 动机：v5 里工作区没有文件系统概念，文件能力靠 `plugins/localfs` 这个"既是 plugin 又是服务"的混合体承载（容器同时实现 `StatefulPlugin` 和 `ServicePlugin`，对外暴露 10 个 tool）。这带来三个问题：
  1. **文件能力绑死在 plugin 形态上** —— 换存储后端（内存、单文件、对象存储）就得重写一整套 plugin + tool 定义
  2. **两套调用入口语义容易漂移** —— `Call`（AI 调用）和 `Funcs()`（函数调用）是两条路径，门控与事件行为可能不一致
  3. **两套投影** —— `Tools()`（LLM 视角）和 `Funcs()`（UI 视角）内容重叠，维护两份
- 变更：
  - **新增 `fs` 包（fs-api）**：纯契约包（只依赖 stdlib），定义 `FileService`（9 个方法）+ 可选扩展 `Watcher` + 约定目录常量 `TmpDir = ".tmp"` / `MemDir = ".mem"` + `FileEntry` / `ListPage` / `Event`
  - **`fs/local`（fs-local）**：从 `plugins/localfs/localfs.go` 平移并改造为 `FileService` 实现。保留全部沙盒能力（词法校验 + 软链/junction 校验 + `realRoot` + `readBrief` / `matchesContent` 二次校验），保留 `Search` 不进目录链接的行为。文件变更监听从 plugin 的 `StatefulPlugin` 生命周期里剥离，改为 `FS` 自己的 `WithWatch` 选项 + `Events()` channel + `Close()`
  - **约定目录写进契约**：`/.tmp` 和 `/.mem` 是每个 fs 实现都必须提供的目录。`OpenWorkspace` 构造完 fs 后 `Stat` 这两个目录，缺一个就 fail-fast —— 未来的 `fs-mem` / `fs-single-file` 在接入点就被卡住
  - **Workspace 接管 fs**：`WorkspaceConfig` 新增 `FS FSConfig`（sealed interface，当前只有 `LocalFSConfig{Path}`），`Workspace` 持有 `fs.FileService` 并通过 `FS()` 暴露；`Close()` 负责释放 watcher
  - **`Call` 成为唯一执行入口**：删除 `Funcs()`。AI 调用和直接函数调用走同一条路径，门控与事件广播完全一致
  - **`Tools()` 成为唯一投影**：删除 `Func` 类型与 `Funcs()`，`ToolInfo` 只保留 `ID` / `Description` / `Schema`
  - **删除 `Internal()`**：可见性开关随双投影一起退场，不再需要
  - **删除 `ReloadablePlugin` / `UpdatePluginConfig` / `ServicePlugin` / `Service()`**：热更新配置和"暴露内部服务对象"两条路都删掉；需要内部服务的实现自己持有引用即可
  - **删除 `tools/builtin` 子包**：`util.echo` 收进 `tools` 包（`tools.Echo()`），仍然 opt-in（不自动注册）
  - **删除 `id` 包**：`id.Init` 只在 `OpenWorkspace` 里被调一次，`id.Next` 没有任何调用方——snowflake ID 工厂没有实际用途，`WorkspaceConfig.WorkerID` 一并删除
  - **删除 `meta` 预留 key**：`plugin/` 命名空间（`KindPlugin` / `PluginKey`）从未被使用，删除
  - **`tools/builtin/service.go` 的 `FileService` / `FileEntry` / `ListPage`** 与 fs-api 重复，删除
  - **`plugins/localfs/plugin.go` 整个容器**（10 个 tool 定义 + `ServicePlugin` / `StatefulPlugin` 实现）删除 —— 文件能力不再是 plugin，而是工作区文件系统
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿；新增 `fs/local/local_test.go` 的 watcher 用例、根包 `ndrive_test.go`（fs 约定目录 / FS 必填 / 读写往返）

### Spec 18: v6 架构重构 —— 去掉 Plugin 容器层

- 动机：Spec 12 引入的 Plugin/Tool 两层模型在实践中没有兑现收益。容器层只提供两样东西：
  1. **id 拼接规则** —— 完整 id = `"<Plugin.ID()>.<Tool.ID()>"`，还要为"单 tool 容器"专门发明"tool 的 ID() 返回空字符串"的约定，以及 `AsPlugin` / `singleToolPlugin` / `bareIDTool` 三个包装类型来兜住这个特例
  2. **共享状态的兄弟 tool** —— 但这件事用闭包捕获就够了（`fsvc := ws.FS()` 然后两个 tool 都捕获 `fsvc`）

  代价却是实打实的：注册表要维护两张表（`tools` + `containers`）和第三张 `lifecycleTargets`；`Register(v any, cfg any)` 的入参是 `any`，类型错误推迟到运行时；`RegisterAll([]Plugin)` 与 `Register` 的行为不对称；容器级依赖无法表达"tool A 依赖 X、tool B 依赖 Y"；文档里每一处 id 都要解释"完整 id 怎么来的"。
- 变更（`tools` 包接口形状全部变化）：
  - **删除 `Plugin` 接口**：`ID() PluginID` + `Tools() []Tool` 整体退场
  - **删除 `SimplePluginDef`**，改为 **`ToolDef`**：字段不变（`ID_` / `Description_` / `Schema_` / `Read_` / `Fn_` / `DependsOn_`），但它只实现 `Tool` + `DependentTool`，不再实现 `Plugin`
  - **删除 `AsPlugin` / `singleToolPlugin` / `bareIDTool`**：三个包装类型连同"空 tool id 表示不拼接"的规则一起删除
  - **`PluginID` → `ToolID`**，`ValidatePluginID` → `ValidateToolID`（规则不变：>= 2 段点分）
  - **错误重命名**：`ErrPluginNotFound` / `ErrPluginDuplicate` / `ErrPluginInvalid` / `ErrPluginCycle` → `ErrToolNotFound` / `ErrToolDuplicate` / `ErrToolInvalid` / `ErrToolCycle`；`CycleError.Cycle` 的元素类型改为 `[]ToolID`
  - **`DependentPlugin` → `DependentTool`**：依赖声明从容器级回到 tool 级（Spec 14 的下放被 Spec 18 撤销），依赖图节点是 tool id
  - **`StatefulPlugin` → `StatefulTool`**：`StartPlugin` / `StopPlugin` 改为 `StartTool` / `StopTool`（方法名带 `Tool` 后缀，避免与用户类型上同名但不同签名的方法意外撞上）
  - **`ToolsAPI.Register(t Tool, cfg any)`**：入参从 `any` 收窄成 `Tool`，编译期就能挡住传错类型；`RegisterAll(ts []Tool)` 同理
  - **`ToolsAPI.StopAllPlugins()` → `StopAll()`**：对所有 `StatefulTool` 调 `StopTool`，按 tool id 字典序停止（顺序确定），错误 `errors.Join` 汇总
  - **`ToolsAPI.Resolve()`** 返回 tool id 的拓扑序（不再是容器 id）
  - **`CallEvent.PluginID` → `CallEvent.ToolID`**（json tag `plugin_id` → `tool_id`）
  - **事件 topic 前缀 `plugin.` → `tool.`**：`tool.<id>.start` / `.done` / `.error`
  - **`Workspace.Register(p any, cfg any)` → `Register(t tools.Tool, cfg any)`**，`Close()` 改调 `StopAll()`
  - **`tools.Echo()` 返回 `Tool`**（原返回 `Plugin`）
  - **删除 `tools` 包里所有 "plugin" 字样**：包注释、`Tool` 注释、`Context` 注释全部改为 tool 措辞
- 影响面：`tools` 包的四个源文件 + 四个测试文件整体重写；根包 `workspace.go`；`permission` / `fs` 的注释措辞；`e2e` 两个测试；`examples/local/{tool_registration,events,permission,storage}` 四个示例；`ARCHITECTURE.md` / `README.md` / `README_en.md`
- 已知遗留：`tools.Context` 仍然只有 `Bus()` / `Checker()` / `API()`，**没有 `FS()`** —— tool 要用工作区文件系统只能靠闭包捕获 `ws.FS()`。`func` 加载器落地时需要决定是把 `FS()` 提到 `tools.Context`（`tools → fs` 依赖成立），还是由加载器在注册时注入
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿；`grep -rni plugin --include=*.go .` 无残留

### Spec 19: v6 架构重构 —— 配置契约开放化 + 权限语义澄清 + 文件变更上总线

- 动机：三件事互相独立，但都指向同一个问题——**框架把选择权收得太紧**。
  1. **配置契约是 sealed 的**：`FSConfig` / `MetaConfig` 带未导出标记方法（`isFSConfig()` / `isMetaConfig()`），只有根包内的类型能实现。用户想接自己的存储后端，只能改根包加分支——而这正是框架应该挡住的"改框架"成本
  2. **权限四档的名字在说谎**：`RAsk < R < WAsk < W` 看起来是一条严格度链，实际四档是读/写两个维度的四象限。`R`（只读）和 `W`（只写）谁更严？取决于操作，没有答案
  3. **文件变更没有出口**：`fs` 实现了变更监听，但事件只走 `Events()` channel——工作区外面（UI、agent 运行时）拿不到，唯一的取法是把 `fs.FileService` 泄漏给订阅方自己 cast

- 变更：

  **（一）配置契约开放化**

  - `fs` 包新增 `Config` 接口：`NewFileService(ctx) (FileService, error)`
  - `meta` 包新增 `Config` 接口：`NewStore(ctx) (Store, error)`
  - **配置对象自己负责初始化**：实现方把"怎么造出自己"写在自己包里，根包不 switch、不认实现类型
  - `fs/local/config.go` 新增 `local.Config{Path, Watch, WatchDebounce, MaxReadSize}`，实现 `fs.Config`
  - `meta/sqlite`、`meta/mysql` 各自的 `Config` 实现 `meta.Config`
  - 根包 `WorkspaceConfig` 收缩成两个字段：`Meta meta.Config` / `FS fs.Config`；`MetaConfig` / `SqliteConfig` / `MysqlConfig` / `FSConfig` / `LocalFSConfig` 全部删除
  - **根包不再 import `fs/local` / `meta/sqlite` / `meta/mysql`**——新增后端 = 新增一个实现包，根包零改动
  - 根包在装配时仍校验 `/.tmp`、`/.mem` 两个约定目录存在（fail-fast），但**创建**由实现方负责

  **（二）权限四档改名 + 语义澄清**

  - `RAsk` → `ModeWriteAsk`（读放行 / 写询问，零值，默认档）
  - `R` → `ModeReadOnly`（读放行 / 写拒绝）
  - `WAsk` → `ModeAskAll`（读写都询问）
  - `W` → `ModeWriteOnly`（写放行 / 读拒绝）
  - **数值不变**（iota 顺序不动）：`0=写询问 1=只读 2=全询问 3=只写`，历史持久化数据一一对应，改名不改值
  - 文档明确写清：这四档**不是一条严格度链**，是读/写两维的四象限
  - `Checker` 改成 **fail-closed**：查不到绑定 / 模式值不认识，一律 `ErrPermissionDenied`，不靠默认值放行

  **（三）删掉 `StatefulTool` 生命周期层**

  - 删除 `StatefulTool` 接口（`StartTool` / `StopTool`）与 `ToolsAPI.StopAll()`
  - `ToolsAPI.Register(t Tool, cfg any)` → `Register(t Tool)`；`Workspace.Register` 同步收窄
  - `RegisterAll` 保留依赖校验 + 拓扑排序 + 一次性 commit，不再有"启动 / 逆序回滚"
  - `Workspace.Close()` 不再停 tool，只关 fs + meta store

  **（四）文件变更接入 bus**

  - `fs` 包新增 `TopicChange = "fs.change"`（跨包约定常量，bus 不支持通配符订阅，所有变更走同一个 topic）
  - `fs.Op` 实现 `MarshalJSON` / `UnmarshalJSON`（字符串形式 `created` / `modified` / `removed` / `renamed`），`fs.Event` 加 json tag——过 bus 到 UI 时人眼可读
  - 根包新增 `fsevents.go`：`bridgeFSEvents(bus, fs)` —— 实现 `fs.Watcher` 时起一个 goroutine，把 `Events()` 的事件 `Publish` 到 `fs.TopicChange`。**`fs` 包不依赖 bus**（存储层不该知道消息总线存在），桥接点集中在一个文件
  - **`fs/local` 的事件合并重写**（`go-filewatcher` 自带的防抖有两个坑：全局防抖把同一批里其它文件的事件全丢掉；按路径防抖只保留最后一条，把"新建文件"抹成"修改"）：
    - 自己在本地按路径合并，窗口内同一路径的多个底层事件合并成一条，**优先级 创建 > 删除 > 修改**
    - **新目录补扫**：底层 watcher 是收到"目录创建"事件之后才把目录挂上监听的，所以"目录建好之后、监听挂上之前"写进去的文件它永远看不到——`Write` 里的"建目录 + 写文件"是挨着执行的，正好落在这个窗口里。收到目录创建事件时把整棵新目录扫一遍，补成创建事件
    - 已报过创建的路径短期内不重复报（补扫和底层事件可能指向同一个文件）

- 影响面：`fs` / `fs/local` / `meta` / `meta/sqlite` / `meta/mysql` / `permission` / `tools` 四个源文件 + 根包 `config.go` / `ndrive.go` / `workspace.go` + 新增 `fsevents.go` / `fs/local/config.go`；`e2e` 测试与全部示例；`ARCHITECTURE.md` / `README.md` / `README_en.md` / `RELEASE.md`
- 验证：`go build ./...` + `go vet ./...` + `go test ./...` 全绿；新增 `fs/local/watch_test.go`（新目录 / 已有目录 / 同批多文件 / 删除 / 约定目录）、`ndrive_customfs_test.go`（自研 fs 后端接入 + 变更事件上总线）

## 实施顺序

v5 阶段（Spec 1–11）按 spec 编号顺序：

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

v6 阶段（Spec 17–19）是三次连续的架构重构，每一刀都改写前一刀的接口形状：

```
Spec 17 (fs-api + 单一执行入口)
   ↓
Spec 18 (去掉 Plugin 容器层)
   ↓
Spec 19 (配置契约开放化 + 权限语义澄清 + 文件变更上总线)
```

Spec 17/18/19 依次落地，后一刀依赖前一刀的结果；每一刀内部按"契约 → 实现 → 测试 → 文档"的顺序推进。
