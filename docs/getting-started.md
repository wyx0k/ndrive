# 快速上手

## 环境准备

```bash
go get github.com/wyx0k/ndrive
```

## 基础用法

```go
import "github.com/wyx0k/ndrive"

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
