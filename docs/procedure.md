# Procedure：业务层复合操作

Op 是 backend 提供的原子存储操作，Procedure 是下游注册的业务层复合操作。两者都受 policy 控制，都出现在 `bm.Ops()`/`bm.Procedures()` 供 LLM tool 动态生成。

## 注册 Procedure

```go
bm.RegisterProcedure(ntd.Procedure{
    Name:        "ppt-to-md",
    Description: "把 PPT 文件转换为 Markdown 并写入树",
    Schema: map[string]any{
        "type":     "object",
        "required": []string{"src", "dst"},
        "properties": map[string]any{
            "src": map[string]any{"type": "string"},
            "dst": map[string]any{"type": "string"},
        },
    },
    Fn: func(ctx context.Context, r ntd.BizmodelResolver, args any) (any, error) {
        m := args.(map[string]any)
        // 可通过 r.GetBizmodel 操作任意 bizmodel
        docs, _ := r.GetBizmodel(ctx, "docs")
        md := convertPPT(m["src"].(string))
        return docs.Call(ctx, "file-fm-write", m["dst"].(string), map[string]any{
            "withfm": true,
            "fm":     map[string]any{"title": filepath.Base(m["src"].(string))},
            "body":   md,
        })
    },
})

// 调用
bm.CallProcedure(ownerCtx, "ppt-to-md", map[string]any{
    "src": "slides.pptx",
    "dst": "/notes/slides.md",
})
```

`BizmodelResolver` 只暴露 `GetBizmodel`，procedure 无法访问 workspace 其他能力。

## 事件发布

执行过程中引擎自动发布：
- `procedure.start` — 开始前
- `procedure.done` — 成功后
- `procedure.error` — 失败时（含 `Err` 字段）

## 动态生成 LLM tools（Op + Procedure 统一）

```go
for _, op := range bm.Ops() {
    tools = append(tools, makeTool(op.Name, op.Description, op.Schema,
        func(args map[string]any) (any, error) {
            return bm.Call(ownerCtx, op.Name, args["path"].(string), args)
        }))
}
for _, proc := range bm.Procedures() {
    tools = append(tools, makeTool(proc.Name, proc.Description, proc.Schema,
        func(args map[string]any) (any, error) {
            return bm.CallProcedure(ownerCtx, proc.Name, args)
        }))
}
```
