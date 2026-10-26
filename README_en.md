# NDrive

[中文版](README.md)

---

The name is inspired by the NT-D (NewType Destroyer) system from Mobile Suit Gundam UC — a drive system activated under specific conditions to respond to intent with extremely high precision. NDrive takes its meaning: precisely responding when permissions are triggered, driving business intent directly to the execution layer.

A plugin registration framework for LLM / agent scenarios. Wrap any capability (storage, network, local tools...) as a plugin attached to a Workspace, and get permission gating, event broadcasting, and LLM tool schema generation for free.

```
go get github.com/wyx0k/ndrive
```

## Core Concepts

| Concept | Description |
|---|---|
| **Workspace** | Independent workspace bound to a userID, holds meta storage, event bus, permission checker and the plugin registry |
| **Plugin** | Plugin container — declares `ID()` and the `Tools()` it hosts; can optionally implement `DependentPlugin` to declare container-level dependencies |
| **Tool** | The actually callable unit: `Description`/`Schema`/`Read`/`Internal` + `Call` |
| **ToolsAPI** | Plugin registry: registration, dependency topo-sort, permission-gated calls, LLM/UI dual-view projection |
| **Permission** | Four permission levels: `RAsk < R < WAsk < W`, configured per user |
| **Bus** | In-process pub/sub + sync request channel |

## Features

- **Plugin/Tool two-layer model**: a Plugin container can host multiple Tools (e.g. a local filesystem capability exposing `list`/`read`/`search`/`stat`), and `Internal()` can hide a Tool so it's only reusable internally without being exposed to LLM/UI
- **Dependency graph + topo-sort**: Plugin containers declare dependencies via the optional `DependentPlugin` interface (`DependsOn() []PluginID`); registration validates them, and batch registration topo-sorts containers automatically — all Tools in a container share the same dependency declaration
- **Four-level permission model**: `RAsk`/`R`/`WAsk`/`W` — `WAsk`/`RAsk` issue a confirmation request over the Bus, suitable for high-risk operations that need human review
- **Event bus**: every Call automatically broadcasts `start`/`done`/`error` lifecycle events; plugins can also publish custom events, making it easy to hook into an LLM pipeline or a UI layer
- **Container lifecycle**: plugins can optionally implement `StatefulPlugin` (start/stop), `ReloadablePlugin` (hot config reload), `ServicePlugin` (expose an internal service object)
- **Ready-to-use local filesystem plugin**: `plugins/localfs` provides file listing/reading/searching/stat/writing/editing/deleting plus file-change watching, ready to register as-is

## Quick Start

```go
import (
    "context"
    "github.com/wyx0k/ndrive"
    "github.com/wyx0k/ndrive/permission"
    "github.com/wyx0k/ndrive/tools"
)

ctx := context.Background()

ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
    WorkerID: 1,
    Meta:     ndrive.SqliteConfig{Path: "./meta.db"},
})
if err != nil {
    // ...
}
defer ws.Close()

// Register the simplest possible plugin (a bare Tool, auto-wrapped into a single-tool container)
ws.Register(tools.SimplePluginDef{
    ID_:          "util.echo",
    Description_: "returns args as-is",
    Read_:        true,
    Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
        return args, nil
    },
}, nil)

// Grant permission, then call
ws.SetPolicy(ctx, "alice", permission.W)
result, _ := ws.API().Call(ctx, "util.echo", "hello")
```

For a multi-tool container (one Plugin hosting several Tools, with start/stop lifecycle), see `plugins/localfs`:

```go
import "github.com/wyx0k/ndrive/plugins/localfs"

fsPlugin, _ := localfs.NewPlugin("/data/notes")
ws.Register(fsPlugin, nil) // triggers StartPlugin automatically, starts watching for file changes

entries, _ := ws.API().Call(ctx, "store.localfs.list", map[string]any{"path": "/"})
```

See [`examples/local/basic_workspace`](examples/local/basic_workspace/main.go) (Workspace lifecycle) and [`examples/local/permission`](examples/local/permission/main.go) (permission approval flow) for complete, runnable examples.

## Documentation

- [Architecture](ARCHITECTURE.md)
- [Implementation Specs](SPECS.md)
