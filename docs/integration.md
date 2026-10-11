# 下游接入示例：LLM + UI 双管线

ntd 只负责存储和权限，LLM 调用和 UI 渲染由下游应用组合。典型结构如下：

```
myapp/
├── main.go          # 组装入口
├── llm/             # LLM pipeline
│   └── agent.go
└── ui/              # UI / API 层
    └── handler.go
```

## 组装入口 main.go

```go
ws, _ := ntd.OpenWorkspace(ctx, "ws-1", "owner", ntd.Config{
    Meta: ntd.SqliteConfig{Path: "./meta.db"},
})

bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
    ID: "docs", TypeName: ntd.BackendLocalFile,
    Config: ntd.LocalFileConfig{RootDir: "./docs"},
})

// 挂载 LLM pipeline
llm.Mount(ws, bm)

// 挂载 UI 层（HTTP / WebSocket 等）
ui.Mount(ws, bm)
```

## LLM pipeline llm/agent.go

LLM 通过 `bm.Call` 写入数据，通过 `bm.Tree()` 读取上下文，通过 bus 接收或发出事件。

```go
func Mount(ws *ntd.Workspace, bm *ntd.Bizmodel) {
    ownerCtx := ws.Context(context.Background())

    // 订阅 UI 发来的用户指令
    ws.Bus().Subscribe("user.instruction", func(payload any) {
        instruction := payload.(string)
        // 调用 LLM，把结果写回树
        result := callLLM(instruction, bm.Tree().Search(ownerCtx, "*"))
        bm.Call(ownerCtx, "file-fm-write", "/output/latest.md", map[string]any{
            "withfm": true,
            "fm":     map[string]any{"title": "LLM Output"},
            "body":   result,
        })
        // 通知 UI 更新
        ws.Bus().Publish("llm.done", result)
    })
}
```

## UI 层 ui/handler.go

UI 接收用户输入，通过 bus 转发给 LLM，同时响应 `policy.ask` 确认请求。

```go
func Mount(ws *ntd.Workspace, bm *ntd.Bizmodel) {
    // 处理 policy 确认弹窗
    ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
        ask := req.(policy.AskPayload)
        approved := showConfirmDialog(ask.Op, ask.Path) // 展示给用户
        return policy.AskResponse{Approved: approved}, nil
    })

    // 订阅 LLM 完成事件，推送到前端
    ws.Bus().Subscribe("llm.done", func(payload any) {
        pushToFrontend(payload.(string))
    })

    // HTTP handler：用户输入 → bus → LLM
    http.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
        instruction := r.FormValue("q")
        ws.Bus().Publish("user.instruction", instruction)
        w.WriteHeader(http.StatusAccepted)
    })
}
```
