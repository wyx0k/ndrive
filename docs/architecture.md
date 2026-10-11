# 架构说明

## 核心概念

**Workspace** — 一个独立的工作空间，绑定一个 owner，持有 meta 存储和事件总线。

**Bizmodel** — 业务模型实例，对应一棵树和一个存储 backend。同一个 workspace 可以挂多个 bizmodel。

**Tree** — 路径树视图，支持按路径读取、列举、模糊搜索、删除。节点的创建通过 op 完成。

**Op** — backend 提供的操作，命名约定 `<subkind>-<动作>`，例如 `dir-mkdir`、`file-fm-write`。

**Policy** — 四档权限：`RAsk < R < WAsk < W`，持久化到 meta，通过 `ws.SetPolicy` 设置。`WAsk/RAsk` 模式下写/读操作会通过事件总线向外请求确认。

**Bus** — 进程内发布/订阅 + 同步请求通道，供下游 LLM pipeline 和 UI 层挂载。

## 数据流

```
用户输入
  → UI Publish("user.instruction")
    → LLM Subscribe，调用 bm.Call 写树
      → policy WAsk 触发 bus.Request("policy.ask")
        → UI Handle 弹窗确认 → 放行
      → 写入成功，Publish("llm.done")
        → UI Subscribe，推送前端
```

ntd 在这个架构里只提供树操作和权限门控，LLM SDK 和 UI 框架的选型完全由下游决定。
