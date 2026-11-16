// Dispatch 实现：Call / Tools。
//
// 行为约定：
//   - Call(ctx, id, args) 4 步：
//     1. permission 门控（按 tool.Read() 路由 CheckRead / CheckWrite）
//     2. 发 `tool.<id>.start` 事件（payload: CallEvent{Args}）
//     3. 调 tool.Call(ctx, ws, args)
//     4. 发 `tool.<id>.done` 或 `tool.<id>.error` 事件
//   - Call 是唯一的执行入口 —— AI 调用和直接函数调用走的是同一条路径，
//     权限门控和事件广播对两者完全一致
//   - Tools() 返 LLM 视角快照（无 Call binding）
//   - 事件 topic 用 `tool.` 前缀（区别于 LLM 工具名 `<id>`）
package tools

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// CallEvent 是 ToolsAPI.Call 在 start / done / error 阶段发的 payload。
// Op: "start" | "done" | "error"。
type CallEvent struct {
	ToolID ToolID    `json:"tool_id"`
	Op     string    `json:"op"`
	Args   any       `json:"args,omitempty"`
	Result any       `json:"result,omitempty"`
	Err    string    `json:"error,omitempty"`
	Time   time.Time `json:"time"`
}

// Call 触发一个 tool（按 tool id）。
// 失败可能来源：tool 未注册 / permission 拒绝 / tool 自身返错。
//
// Call 是唯一的执行入口：AI 调用（拿着 Tools() 的 schema 生成参数）和直接
// 函数调用走的是同一段代码，权限门控与事件广播对两者一视同仁。
func (a *ToolsAPI) Call(ctx context.Context, id ToolID, args any) (any, error) {
	a.mu.RLock()
	t, ok := a.tools[id]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, id)
	}

	// 1. permission 门控
	if a.ws != nil {
		if c := a.ws.Checker(); c != nil {
			var err error
			if t.Read() {
				err = c.CheckRead(ctx, id)
			} else {
				err = c.CheckWrite(ctx, id)
			}
			if err != nil {
				return nil, err
			}
		}
	}

	// 2. start 事件
	a.publishEvent(id, "start", args, nil, nil)

	// 3. tool.Call
	result, err := t.Call(ctx, a.ws, args)

	// 4. done / error 事件
	if err != nil {
		a.publishEvent(id, "error", args, nil, err)
		return result, err
	}
	a.publishEvent(id, "done", nil, result, nil)
	return result, nil
}

// Tools 返 LLM 视角的 tool 列表（按 tool id 字典序）。
// 字段是只读快照；不能用于实际调用 —— 实际调用走 Call。
func (a *ToolsAPI) Tools() []ToolInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]ToolID, 0, len(a.tools))
	for id := range a.tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ToolInfo, 0, len(ids))
	for _, id := range ids {
		t := a.tools[id]
		out = append(out, ToolInfo{
			ID:          id,
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return out
}

// publishEvent 发 `tool.<id>.<op>` 事件。
// a.ws 或 a.ws.Bus() 为 nil 时静默跳过（不阻塞 Call 主流程）。
func (a *ToolsAPI) publishEvent(id, op string, args, result any, err error) {
	if a.ws == nil {
		return
	}
	b := a.ws.Bus()
	if b == nil {
		return
	}
	evt := CallEvent{
		ToolID: id,
		Op:     op,
		Args:   args,
		Result: result,
		Time:   time.Now(),
	}
	if err != nil {
		evt.Err = err.Error()
	}
	b.Publish("tool."+id+"."+op, evt)
}
