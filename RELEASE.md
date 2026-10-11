# 发布说明

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
- 四档权限模型（RAsk < R < WAsk < W）
- 进程内发布/订阅事件总线
- 灵活的 Backend 扩展 API
