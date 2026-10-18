// Dispatch 实现：Call / Tools / Funcs。
//
// v5 行为约定：
//   - Call(ctx, id, args) 4 步：
//     1. permission 门控（按 plugin.Read() 路由 CheckRead / CheckWrite）
//     2. 发 `plugin.<id>.start` 事件（payload: CallEvent{Args}）
//     3. 调 plugin.Call(ctx, ws, args)
//     4. 发 `plugin.<id>.done` 或 `plugin.<id>.error` 事件
//   - Tools() 返 LLM 视角快照（无 Call binding）
//   - Funcs() 返 UI 视角快照（Call 已 bind 到当前 api）
//   - 事件 topic 用 `plugin.` 前缀（区别于 LLM 工具名 `<id>`）
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
	PluginID PluginID `json:"plugin_id"`
	Op       string   `json:"op"`
	Args     any      `json:"args,omitempty"`
	Result   any      `json:"result,omitempty"`
	Err      string   `json:"error,omitempty"`
	Time     time.Time `json:"time"`
}

// Call 触发一个 plugin。
// 失败可能来源：plugin 未注册 / permission 拒绝 / plugin 自身返错。
func (a *ToolsAPI) Call(ctx context.Context, id PluginID, args any) (any, error) {
	p, ok := a.plugins[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}

	// 1. permission 门控
	if a.ws != nil {
		if c := a.ws.Checker(); c != nil {
			var err error
			if p.Read() {
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

	// 3. plugin.Call
	result, err := p.Call(ctx, a.ws, args)

	// 4. done / error 事件
	if err != nil {
		a.publishEvent(id, "error", args, nil, err)
		return result, err
	}
	a.publishEvent(id, "done", nil, result, nil)
	return result, nil
}

// UpdatePluginConfig 运行中更新一个 plugin 的配置。
// plugin 必须实现 ReloadablePlugin，否则返 error。
// 更新成功后发 `plugin.<id>.config_updated` 事件（payload: CallEvent{Args: cfg}）；
// 更新失败不发事件。
func (a *ToolsAPI) UpdatePluginConfig(id PluginID, cfg any) error {
	p, ok := a.plugins[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	cp, ok := p.(ReloadablePlugin)
	if !ok {
		return fmt.Errorf("tools: plugin %s does not implement ReloadablePlugin", id)
	}
	if err := cp.UpdateConfig(cfg); err != nil {
		return err
	}
	a.publishEvent(id, "config_updated", cfg, nil, nil)
	return nil
}

// Tools 返 LLM 视角的 plugin 列表（按 id 字典序）。
// 字段是只读快照；不能用于实际调用。
func (a *ToolsAPI) Tools() []Tool {
	ids := make([]PluginID, 0, len(a.plugins))
	for id := range a.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Tool, 0, len(ids))
	for _, id := range ids {
		p := a.plugins[id]
		out = append(out, Tool{
			ID:          p.ID(),
			Description: p.Description(),
			Schema:      p.Schema(),
		})
	}
	return out
}

// Funcs 返 UI 视角的 plugin 列表（按 id 字典序）。
// 每个 Func.Call 已 bind 到当前 api，可直接调用（permission 门控仍走 api.Call）。
func (a *ToolsAPI) Funcs() []Func {
	ids := make([]PluginID, 0, len(a.plugins))
	for id := range a.plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Func, 0, len(ids))
	for _, id := range ids {
		// capture per-iteration
		id, p := id, a.plugins[id]
		out = append(out, Func{
			ID: id,
			Call: func(ctx context.Context, args any) (any, error) {
				return a.Call(ctx, id, args)
			},
		})
		_ = p // plugin 信息已在 closure 内通过 id 路由
	}
	return out
}

// publishEvent 发 `plugin.<id>.<op>` 事件。
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
		PluginID: id,
		Op:       op,
		Args:     args,
		Result:   result,
		Time:     time.Now(),
	}
	if err != nil {
		evt.Err = err.Error()
	}
	b.Publish("plugin."+id+"."+op, evt)
}
