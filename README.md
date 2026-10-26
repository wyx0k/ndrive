# NDrive

[English Version](README_en.md)

---

名称灵感来自《高达 UC》中的 NT-D（NewType Destroyer）系统——一套在特定条件下被激活、以极高精度响应意图的驱动系统。NDrive 取其意：在权限触发时精准响应，把业务意图直接驱动到执行层。

一个面向 LLM / Agent 场景的插件注册框架。把任意能力（存储、网络、本地工具……）包装成 plugin 挂到 Workspace 上，统一走权限校验、事件广播，并自动生成 LLM tool schema。

```
go get github.com/wyx0k/ndrive
```

## 核心概念

| 概念 | 说明 |
|---|---|
| **Workspace** | 独立工作空间，绑定 userID，持有 meta 存储、事件总线、权限 Checker 和插件注册中心 |
| **Plugin** | 插件容器，只负责声明 `ID()` 和挂载的 `Tools()` 列表 |
| **Tool** | 真正可调用的单元：`Description`/`Schema`/`DependsOn`/`Read`/`Internal` + `Call` |
| **ToolsAPI** | 插件注册中心：注册、依赖拓扑排序、权限门控调用、LLM/UI 双视图投影 |
| **Permission** | 四档权限：`RAsk < R < WAsk < W`，按 user 维度配置 |
| **Bus** | 进程内发布/订阅 + 同步请求通道 |

## 特性

- **Plugin/Tool 二层模型**：一个 Plugin 容器下可以挂多个 Tool（比如一套文件系统能力的 `list`/`read`/`search`/`stat`），也可以用 `Internal()` 把某个 Tool 隐藏成"只给内部复用、不直接暴露给 LLM/UI"
- **依赖图 + 拓扑排序**：Tool 之间用 `DependsOn` 声明依赖，注册时自动校验、批量注册时自动排序
- **四档权限模型**：`RAsk`/`R`/`WAsk`/`W`，`WAsk`/`RAsk` 会通过 Bus 向外发起确认请求，适合需要人审的高风险操作
- **事件总线**：每次 Call 自动广播 `start`/`done`/`error` 生命周期事件，插件内部也能发自定义事件，方便 LLM pipeline 和 UI 层挂载
- **容器生命周期**：插件可选实现 `StatefulPlugin`（启动/停止）、`ReloadablePlugin`（热更新配置）、`ServicePlugin`（暴露内部服务对象）
- **开箱即用的本地文件插件**：`plugins/localfs` 提供文件列举/读取/搜索/stat/写入/编辑 + 文件变更监听，可直接注册使用

## 快速开始

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

// 注册一个最简单的 plugin（裸 Tool，自动包装成单 tool 容器）
ws.Register(tools.SimplePluginDef{
    ID_:          "util.echo",
    Description_: "原样返回 args",
    Read_:        true,
    Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
        return args, nil
    },
}, nil)

// 授权后调用
ws.SetPolicy(ctx, "alice", permission.W)
result, _ := ws.API().Call(ctx, "util.echo", "hello")
```

多 tool 容器（一个 Plugin 挂多个 Tool，并带启动/停止生命周期）直接参考 `plugins/localfs`：

```go
import "github.com/wyx0k/ndrive/plugins/localfs"

fsPlugin, _ := localfs.NewPlugin("/data/notes")
ws.Register(fsPlugin, nil) // 自动触发 StartPlugin，开始监听文件变更

entries, _ := ws.API().Call(ctx, "store.localfs.list", map[string]any{"path": "/"})
```

更完整的可运行示例见 [`examples/local/basic_workspace`](examples/local/basic_workspace/main.go)（Workspace 生命周期）和 [`examples/local/permission`](examples/local/permission/main.go)（权限审批流程）。

## 文档

- [架构说明](ARCHITECTURE.md)
- [实施 Specs](SPECS.md)
