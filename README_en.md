# NDrive

[中文版](README.md)

---

The name is inspired by the NT-D (NewType Destroyer) system from Mobile Suit Gundam UC — a drive system activated under specific conditions to respond to intent with extremely high precision. NDrive takes its meaning: precisely responding when permissions are triggered, driving business intent directly to the execution layer.

A tool registration framework for LLM / agent scenarios. Wrap any capability (storage, network, local tools...) as a tool attached to a Workspace, and get permission gating, event broadcasting, and LLM tool schema generation for free.

```
go get github.com/wyx0k/ndrive
```

## Core Concepts

| Concept | Description |
|---|---|
| **Workspace** | Independent workspace bound to a userID, holds meta storage, event bus, permission checker, workspace filesystem and the tool registry |
| **Tool** | The only callable unit: `ID`/`Description`/`Schema`/`Read` + `Call`; may optionally implement `DependentTool` (declare dependencies) |
| **ToolsAPI** | Tool registry: registration, dependency topo-sort, permission-gated calls, LLM-view projection |
| **fs-api** | Workspace filesystem contract (`fs.FileService`): list/read/search/write/edit/delete plus optional change watching (`fs.Watcher`) |
| **Permission** | Four permission levels: read-only / write-only / write-asks / asks-everything, configured per user |
| **Bus** | In-process pub/sub + sync request channel |

## Features

- **Single execution entry point**: `Call` is the only way in — an AI call (generating arguments from the `Tools()` schema) and a direct function call take the exact same path, with identical permission gating and event broadcasting
- **One layer only**: the Tool is the unit of registration; there is no container layer. A tool's `ID()` is the registry key and the call name; tools that need to share state capture the same instance in a closure
- **Dependency graph + topo-sort**: tools declare dependencies via the optional `DependentTool` interface (`DependsOn() []ToolID`); registration validates them, and batch registration topo-sorts tools automatically
- **Four-level permission model**: `ModeReadOnly`/`ModeWriteOnly`/`ModeWriteAsk`/`ModeAskAll` — the last two issue a confirmation request over the Bus, suitable for high-risk operations that need human review
- **Event bus**: every Call automatically broadcasts `start`/`done`/`error` lifecycle events (topic `tool.<id>.*`); tools can also publish custom events, making it easy to hook into an LLM pipeline or a UI layer
- **Workspace ships a filesystem**: every Workspace holds one `fs.FileService`; the implementation is the user's choice (currently only the local directory `fs/local`). Its root always contains the two convention directories `/.tmp` (scratch space) and `/.mem` (long-term memory)
- **File changes reach the bus**: with watching enabled, workspace file changes are broadcast on the `fs.change` topic — a UI or agent runtime just subscribes

## Quick Start

```go
import (
    "context"

    "github.com/wyx0k/ndrive"
    "github.com/wyx0k/ndrive/fs/local"
    "github.com/wyx0k/ndrive/meta/sqlite"
    "github.com/wyx0k/ndrive/permission"
    "github.com/wyx0k/ndrive/tools"
)

ctx := context.Background()

ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
    Meta: sqlite.Config{Path: "./meta.db"},
    FS:   local.Config{Path: "./ws"},
})
if err != nil {
    // ...
}
defer ws.Close()

// Register the simplest possible tool
ws.Register(tools.ToolDef{
    ID_:          "util.echo",
    Description_: "returns args as-is",
    Read_:        true,
    Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
        return args, nil
    },
})

// Grant permission, then call
ws.SetPolicy(ctx, "alice", permission.ModeWriteOnly)
result, _ := ws.API().Call(ctx, "util.echo", "hello")
```

Backends are handed in as configuration objects — `sqlite.Config` / `mysql.Config` / `local.Config` each initialize themselves into the matching backend, and you can plug in your own implementation instead (`meta.Config` and `fs.Config` are open contracts; the root package knows no concrete implementation).

---

## Current Architecture

### Layers

Dependencies flow one way, no cycles:

```
External system (agent / llm / ui)
   ↓ Call(ctx, id, args)
ndrive root package (pure assembly: OpenWorkspace + Workspace)
   ├── bus        in-process pub/sub + req/resp
   ├── permission 4-level gating + human-approval channel (over bus)
   ├── tools      registry: registration / topo-sort / single execution entry / LLM projection
   ├── meta       KV persistence + typed facade (workspace ownership, permission bindings)
   └── fs         filesystem contract + convention dirs /.tmp, /.mem
```

The root package **knows no concrete implementation** — the `meta` / `fs` backends arrive as configuration objects and initialize themselves. A new backend = a new implementation package; the root package changes zero lines.

### Three core paths

| Path | Entry | What it does |
|---|---|---|
| Call | `ws.API().Call(ctx, id, args)` | The one execution path: permission gate → `tool.<id>.start` → execute → `done`/`error`. An AI call and a direct function call take the same route |
| Projection | `ws.API().Tools()` | The one LLM-facing view (id + description + json schema), sorted by id |
| Events | `ws.Bus()` | `tool.<id>.*` lifecycle, `permission.ask` sync requests, `fs.change` file changes |

### The four permission levels

`Mode` is persisted as an iota integer — the rename does not change the values, so existing data keeps working. The four levels are a **quadrant over two dimensions (read / write), not a strictness chain** — which one is stricter depends on the operation.

| mode | read op | write op |
|---|---|---|
| `ModeWriteAsk` (zero value, default) | allow | ask |
| `ModeReadOnly` | allow | deny |
| `ModeAskAll` | ask | ask |
| `ModeWriteOnly` | deny | allow |

Gating is **fail-closed**: an unknown binding or an unrecognized mode value is denied (`permission.ErrPermissionDenied`) — there is no "fall back to the default and allow" hole.

---

## Integrating

### 1. Plug in your own storage backend

Implement one of the contracts with a `Config` type; the root package needs no change:

```go
// implements meta.Config
type MyMetaConfig struct{ ... }
func (c MyMetaConfig) NewStore(ctx context.Context) (meta.Store, error) { ... }

// implements fs.Config — must guarantee /.tmp and /.mem exist, otherwise
// OpenWorkspace fails fast
type MyFSConfig struct{ ... }
func (c MyFSConfig) NewFileService(ctx context.Context) (fs.FileService, error) { ... }
```

Then hand them to `WorkspaceConfig{Meta: ..., FS: ...}` as usual.

### 2. Subscribe to events

A UI or agent runtime just subscribes:

```go
ws.Bus().Subscribe(fs.TopicChange, func(p any) { /* fs.Event */ })
ws.Bus().Subscribe("tool.util.echo.done", func(p any) { /* tools.CallEvent */ })
```

Topic conventions:

| topic | payload | when |
|---|---|---|
| `tool.<id>.start` / `.done` / `.error` | `tools.CallEvent` | broadcast automatically on every Call |
| `permission.ask` | `AskPayload` → `AskResponse` | permission request (req/resp) |
| `fs.change` | `fs.Event` | a workspace file changed |

bus has no wildcard subscriptions, so file changes share a single topic — the change type is in the payload's `Op` (`created` / `modified` / `removed` / `renamed`).

### 3. Wire up human approval

Subscribe to `permission.ask` and answer with `AskResponse{Approved}`:

```go
ws.Bus().Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
    p := req.(permission.AskPayload)
    return permission.AskResponse{Approved: askHuman(p)}, nil
})
```

The `Handle` handler is the end of ctx propagation — it has to watch `ctx.Done()` itself.

### 4. One tool calling another

`tools.Context` carries `Bus()` / `Checker()` / `API()`, so just call `ws.API().Call(...)` — it goes through the same gate and broadcasts the same events.

### 5. Reach the workspace filesystem

`ws.FS()` returns the `fs.FileService` interface — local directory, in-memory or your own implementation, the caller does not care:

```go
fsvc := ws.FS()

// write a file
entry, _ := fsvc.Write(ctx, "notes/hello.txt", []byte("hello ndrive"))

// read it back
data, _ := fsvc.Read(ctx, "notes/hello.txt")
```

Note that `tools.Context` has **no** `FS()` — a tool that needs the filesystem has to capture it in a closure at registration time:

```go
fsvc := ws.FS()
ws.Register(tools.ToolDef{
    ID_: "store.localfile.read", Description_: "read a file", Read_: true,
    Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
        return fsvc.Read(ctx, pathFrom(args))
    },
})
```

See [`examples/local/basic_workspace`](examples/local/basic_workspace/main.go) (Workspace lifecycle), [`examples/local/tool_registration`](examples/local/tool_registration/main.go) (registration and calling), [`examples/local/storage`](examples/local/storage/main.go) (reading and writing files through the workspace filesystem), [`examples/local/events`](examples/local/events/main.go) (the event bus) and [`examples/local/permission`](examples/local/permission/main.go) (the four permission levels) for complete, runnable examples.

## Documentation

- [Architecture](ARCHITECTURE.md)
- [Implementation Specs](SPECS.md)
