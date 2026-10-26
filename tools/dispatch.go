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

// Call 触发一个 tool（按完整 id）。
// 失败可能来源：tool 未注册 / permission 拒绝 / tool 自身返错。
// 对 Internal() == true 的 tool 同样有效——Internal 只影响 Tools()/Funcs()
// 的可见性，不影响能否被 Call（供其他 plugin 内部调用）。
func (a *ToolsAPI) Call(ctx context.Context, id PluginID, args any) (any, error) {
	a.mu.RLock()
	p, ok := a.tools[id]
	a.mu.RUnlock()
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

// UpdatePluginConfig 运行中更新一个 plugin 容器的配置（按容器 id，不是
// tool 完整 id——单 tool 容器时两者相同）。
// plugin 必须实现 ReloadablePlugin，否则返 error。
// 更新成功后发 `plugin.<容器id>.config_updated` 事件（payload: CallEvent{Args: cfg}）；
// 更新失败不发事件。
func (a *ToolsAPI) UpdatePluginConfig(id PluginID, cfg any) error {
	a.mu.RLock()
	target, ok := a.lifecycleTargets[id]
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	cp, ok := target.(hasUpdateConfig)
	if !ok {
		return fmt.Errorf("tools: plugin %s does not implement ReloadablePlugin", id)
	}
	if err := cp.UpdateConfig(cfg); err != nil {
		return err
	}
	a.publishEvent(id, "config_updated", cfg, nil, nil)
	return nil
}

// Service 返回已注册 plugin 容器暴露的原生能力（ServicePlugin.Service()），
// 按容器 id 查找（单 tool 容器时容器 id 就是 tool 的完整 id）。
// 用于 plugin 间高频/流式协作，跳过 Call 的 schema/permission/事件通道。
// 调用方通常在自己的 DependsOn_ 声明依赖该 id，Register 阶段已保证其存在，
// 这里只做查找 + 接口断言。
func (a *ToolsAPI) Service(id PluginID) (any, error) {
	a.mu.RLock()
	target, ok := a.lifecycleTargets[id]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	sp, ok := target.(hasService)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPluginNoService, id)
	}
	return sp.Service(), nil
}

// Tools 返 LLM 视角的 tool 列表（按完整 id 字典序），排除 Internal() == true 的 tool。
// 字段是只读快照；不能用于实际调用。
func (a *ToolsAPI) Tools() []ToolInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]PluginID, 0, len(a.tools))
	for id := range a.tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ToolInfo, 0, len(ids))
	for _, id := range ids {
		t := a.tools[id]
		if t.Internal() {
			continue
		}
		out = append(out, ToolInfo{
			ID:          id,
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return out
}

// Funcs 返 UI 视角的 tool 列表（按完整 id 字典序），排除 Internal() == true 的 tool。
// 每个 Func.Call 已 bind 到当前 api，可直接调用（permission 门控仍走 api.Call）。
func (a *ToolsAPI) Funcs() []Func {
	a.mu.RLock()
	ids := make([]PluginID, 0, len(a.tools))
	for id, t := range a.tools {
		if t.Internal() {
			continue
		}
		ids = append(ids, id)
	}
	a.mu.RUnlock()
	sort.Strings(ids)
	out := make([]Func, 0, len(ids))
	for _, id := range ids {
		// capture per-iteration
		id := id
		out = append(out, Func{
			ID: id,
			Call: func(ctx context.Context, args any) (any, error) {
				return a.Call(ctx, id, args)
			},
		})
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
