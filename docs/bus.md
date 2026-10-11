# 事件总线

Bus 提供两种模式：

## 单向广播（fire-and-forget）

```go
bus := ws.Bus()

_, unsub := bus.Subscribe("my.topic", func(payload any) {
    fmt.Println("received:", payload)
})
defer unsub()

bus.Publish("my.topic", "hello")
```

## 请求/回复（用于 `WAsk/RAsk` policy 确认）

```go
unsub := ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
    ask := req.(policy.AskPayload)
    // 展示给用户或 LLM 决策
    approved := askUser(ask.Op, ask.Path)
    return policy.AskResponse{Approved: approved}, nil
})
defer unsub()
```

## 引擎保留 topic

| Topic | 触发时机 | Payload 类型 | 返回类型 |
|---|---|---|---|
| `policy.ask` | policy 为 `WAsk`/`RAsk` 时每次操作前 | `policy.AskPayload` | `policy.AskResponse` |
| `procedure.start` | procedure 开始执行时 | `ntd.ProcedureEvent` | — |
| `procedure.done` | procedure 执行成功时 | `ntd.ProcedureEvent` | — |
| `procedure.error` | procedure 执行失败时 | `ntd.ProcedureEvent`（含 Err）| — |

### ntd.ProcedureEvent 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `Name` | string | procedure 名称 |
| `BizmodelID` | string | 所属 bizmodel ID |
| `Err` | error | 仅 `procedure.error` 时非 nil |

### ntd.UpdateEvent 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `Path` | string | 变更节点路径，如 `/notes/hello.md` |
| `Op` | string | `create` / `write` / `delete` / `rename` |

### policy.AskPayload 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `WorkspaceID` | string | 当前 workspace ID |
| `OwnerID` | string | 发起操作的 owner |
| `Op` | string | 操作名，如 `dir-mkdir` |
| `Path` | string | 操作路径 |
| `Preview` | string | 操作内容摘要（可选） |

### policy.AskResponse 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `Approved` | bool | `true` 放行，`false` 拒绝 |

> **重要**：使用 `WAsk` 或 `RAsk` 模式时，必须通过 `bus.Handle("policy.ask", ...)` 注册 handler，否则所有操作都会被直接拒绝。

Bus 是进程内通道，适合 LLM pipeline 和 UI 层挂载。跨进程场景由下游自行桥接。
