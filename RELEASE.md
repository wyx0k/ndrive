# 发布说明

## 未发布（v6 架构重构）

### 破坏性变更

- **配置契约开放化** —— `ndrive.SqliteConfig` / `ndrive.MysqlConfig` / `ndrive.LocalFSConfig` 删除，改用各自实现包里的配置对象：
  - `sqlite.Config{...}` / `mysql.Config{...}` / `local.Config{...}`
  - `WorkspaceConfig{Meta: meta.Config, FS: fs.Config}` —— `meta.Config` / `fs.Config` 是**开放契约**（`NewStore(ctx)` / `NewFileService(ctx)`），可以接自研实现，根包零改动
  - 根包不再 import 任何具体后端包
- **权限四档改名**（数值不变，历史数据不受影响）：
  - `permission.RAsk` → `permission.ModeWriteAsk`（读放行 / 写询问，默认档）
  - `permission.R` → `permission.ModeReadOnly`（读放行 / 写拒绝）
  - `permission.WAsk` → `permission.ModeAskAll`（读写都询问）
  - `permission.W` → `permission.ModeWriteOnly`（写放行 / 读拒绝）
  - 这四档**不是一条严格度链**，是读/写两个维度的四象限
- **删除 `StatefulTool` 生命周期层** —— `tools.StatefulTool` 接口与 `ToolsAPI.StopAll()` 移除；`Register` 不再收 `cfg` 参数：`ws.Register(tool)` / `ws.API().Register(tool)`

### 新增功能

- **文件变更事件接入 bus** —— 开启文件监听后，工作区的文件变更以 `fs.change` 为 topic 广播到 Bus（payload 是 `fs.Event`，`Op` 序列化成 `created` / `modified` / `removed` / `renamed`），UI / agent 运行时直接订阅即可
- **`fs/local` 事件合并重写** —— 修掉两个丢事件的坑：同一批里改多个文件不再只报最后一个；新建目录里的文件不再漏报（底层 watcher 挂上监听之前写进去的文件由本地补扫兜住）

### Bug 修复

- 约定目录 `/.tmp`、`/.mem` 是点目录，底层文件监听默认跳过点目录 —— 工作区记忆目录里的变更永远不会发出去。现在显式关掉该行为
- 权限门控改成 fail-closed：查不到绑定或模式值不认识时一律拒绝，不再有"默认值放行"的口子

## v0.1.1 (2026-10-11)

### 概述

NDrive 首个正式版本发布 —— 树形业务模型存储引擎。

### 新增功能

- **Procedure** - 业务层复合操作注册，与 Op 统一暴露给 LLM tool 生成
- **E2E 测试** - 新增端到端测试覆盖
- **LocalFile Backend 优化** - 本地文件后端性能调优
- **模块名调整** - 优化模块命名，提升可读性

### Bug 修复

- 修复事件总线（bus）处理问题

### 核心特性

- 树形结构组织业务数据
- 多后端支持：LocalFile、SQLite、BadgerDB、远端 MySQL/MongoDB
- 四档权限模型（只读 / 只写 / 写询问 / 全询问）
- 进程内发布/订阅事件总线
- 灵活的 Backend 扩展 API
