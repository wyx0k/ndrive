# NDrive

[English Version](README_en.md)

---

名称灵感来自《高达 UC》中的 NT-D（NewType Destroyer）系统——一套在特定条件下被激活、以极高精度响应意图的驱动系统。NDrive 取其意：在权限触发时精准响应，把业务意图直接驱动到存储层。

树形业务模型存储引擎。把任意业务数据组织成路径树，统一读写、搜索和权限控制。

```
go get github.com/wyx0k/ndrive
```

## 核心概念

| 概念 | 说明 |
|---|---|
| **Workspace** | 独立工作空间，绑定 owner，持有 meta 存储和事件总线 |
| **Bizmodel** | 业务模型实例，对应一棵树和一个存储 backend |
| **Tree** | 路径树视图，按路径读取、列举、模糊搜索、删除 |
| **Op** | backend 提供的原子操作，命名约定 `<subkind>-<动作>` |
| **Policy** | 四档权限：`RAsk < R < WAsk < W` |
| **Bus** | 进程内发布/订阅 + 同步请求通道 |

## 特性

- **树形结构**：把业务数据组织成路径树，统一读写、搜索和权限控制
- **多 Backend**：内置 LocalFile、SQLite、BadgerDB，远端 MySQL/MongoDB，自定义扩展
- **灵活权限**：四档权限模型，支持 `WAsk`/`RAsk` 向外请求确认
- **事件总线**：进程内 pub/sub，用于 LLM pipeline 和 UI 层挂载
- **Procedure**：业务层复合操作注册，与 Op 统一暴露给 LLM tool 生成

## 内置 Backend

| 常量 | 存储 |
|---|---|
| `BackendLocalFile` | 本地文件系统 |
| `BackendLocalSQLite` | 本地 SQLite |
| `BackendLocalBadger` | 本地 BadgerDB |
| `BackendRemoteSQL` | 远端 SQL（MySQL 等） |
| `BackendRemoteMongo` | 远端 MongoDB |

## 快速开始

```go
import "github.com/wyx0k/ndrive"

ctx := context.Background()

ws, err := ntd.OpenWorkspace(ctx, "ws-1", "owner-alice", ntd.Config{
    Meta: ntd.SqliteConfig{Path: "./meta.db"},
})
defer ws.Close()

bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
    ID:       "notes",
    TypeName: ntd.BackendLocalFile,
    Config:   ntd.LocalFileConfig{RootDir: "./notes"},
})

ws.SetPolicy(ctx, ntd.W)
ownerCtx := ws.Context(ctx)

bm.Call(ownerCtx, "dir-mkdir", "/2024", nil)
bm.Call(ownerCtx, "file-fm-write", "/2024/note.md", map[string]any{
    "withfm": true,
    "fm":     map[string]any{"title": "My Note"},
    "body":   "# My Note\n\nHello.",
})

briefs, _ := bm.Tree().ListWithBrief(ownerCtx, "/2024")
results, _ := bm.Tree().Search(ownerCtx, "*note*")
```

## 文档

详细文档见 [docs/](docs/) 目录：

- [快速上手](docs/getting-started.md)
- [架构说明](docs/architecture.md)
- [Backend 扩展](docs/backend.md)
- [事件总线](docs/bus.md)
- [Procedure](docs/procedure.md)
- [下游接入示例](docs/integration.md)
