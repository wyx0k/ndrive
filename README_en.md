# NDrive

[中文版](README.md)

---

The name is inspired by the NT-D (NewType Destroyer) system from Mobile Suit Gundam UC — a drive system activated under specific conditions to respond to intent with extremely high precision. NDrive takes its meaning: precisely responding when permissions are triggered, driving business intent directly to the storage layer.

A tree-based business model storage engine. Organize arbitrary business data into a path tree with unified read/write, search, and access control.

```
go get github.com/wyx0k/ndrive
```

## Core Concepts

| Concept | Description |
|---|---|
| **Workspace** | Independent workspace bound to an owner, holds meta storage and event bus |
| **Bizmodel** | Business model instance, corresponds to a tree and a storage backend |
| **Tree** | Path tree view with read by path, list, fuzzy search, and delete |
| **Op** | Atomic storage operations provided by backend, naming convention `<subkind>-<action>` |
| **Policy** | Four permission levels: `RAsk < R < WAsk < W` |
| **Bus** | In-process pub/sub + sync request channel |

## Features

- **Tree Structure**: Organize business data into path trees with unified read/write, search, and access control
- **Multiple Backends**: Built-in LocalFile, SQLite, BadgerDB, remote MySQL/MongoDB, custom extensions
- **Flexible Permissions**: Four-level permission model with `WAsk`/`RAsk` for external confirmation
- **Event Bus**: In-process pub/sub for LLM pipeline and UI layer attachment
- **Procedure**: Business-level composite operations registration, exposed to LLM tool generation alongside Op

## Built-in Backends

| Constant | Storage |
|---|---|
| `BackendLocalFile` | Local filesystem |
| `BackendLocalSQLite` | Local SQLite |
| `BackendLocalBadger` | Local BadgerDB |
| `BackendRemoteSQL` | Remote SQL (MySQL, etc.) |
| `BackendRemoteMongo` | Remote MongoDB |

## Quick Start

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

## Documentation

Detailed documentation is available in the [docs/](docs/) directory:

- [Getting Started](docs/getting-started.md)
- [Architecture](docs/architecture.md)
- [Backend Extension](docs/backend.md)
- [Event Bus](docs/bus.md)
- [Procedure](docs/procedure.md)
- [Integration Examples](docs/integration.md)
