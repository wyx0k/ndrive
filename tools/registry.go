// Registry 实现：Register / RegisterAll / Resolve。
//
// v5 行为约定：
//   - Register 立即校验 id 合法 + 不重名 + 所有 DependsOn 已注册（fail-fast）
//   - RegisterAll 接收一组 plugin，按 DependsOn 拓扑排序后逐个 Register
//   - Resolve 用 DFS 拓扑排序 + 环检测，返环路径（依赖在前，执行顺序）
//   - 错误：ErrPluginNotFound / ErrPluginDuplicate / ErrPluginInvalid / ErrPluginCycle
//   - CycleError 是 typed error，Wrap 到 ErrPluginCycle
package tools

import (
	"errors"
	"fmt"
	"sort"
)

// 公共错误。
var (
	ErrPluginNotFound  = errors.New("tools: plugin not found")
	ErrPluginDuplicate = errors.New("tools: plugin already registered")
	ErrPluginInvalid   = errors.New("tools: plugin invalid")
	ErrPluginCycle     = errors.New("tools: dependency cycle")
)

// CycleError 是 RegisterAll / Resolve 检测到依赖环时的 typed error。
// 用 errors.Is(err, ErrPluginCycle) 可统一判定。
type CycleError struct {
	Cycle []PluginID // 环上节点（按顺序，环回到起点）
}

func (e *CycleError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("tools: dependency cycle: %v", e.Cycle)
}

func (e *CycleError) Is(target error) bool {
	return target == ErrPluginCycle
}

// Register 注册一个 plugin。
// 校验顺序：id 合法 → 不重名 → 所有 DependsOn 已注册。
// 若 plugin 实现了 StatefulPlugin，校验通过后用 cfg 作为初始配置调 StartPlugin
// （不需要初始配置传 nil）。失败时 registry 不变（不部分修改）。
func (a *ToolsAPI) Register(plugin Plugin, cfg any) error {
	if plugin == nil {
		return fmt.Errorf("%w: nil plugin", ErrPluginInvalid)
	}
	id := plugin.ID()
	if err := ValidatePluginID(id); err != nil {
		return fmt.Errorf("%w: %v", ErrPluginInvalid, err)
	}
	if _, exists := a.plugins[id]; exists {
		return fmt.Errorf("%w: %s", ErrPluginDuplicate, id)
	}
	// 所有依赖必须已注册（forward ref 禁止；Spec 3 walkthrough 决定 C）
	for _, dep := range plugin.DependsOn() {
		if _, ok := a.plugins[dep]; !ok {
			return fmt.Errorf("%w: %s (needed by %s)", ErrPluginNotFound, dep, id)
		}
	}
	if sp, ok := plugin.(StatefulPlugin); ok {
		if err := sp.StartPlugin(a.ws, cfg); err != nil {
			return fmt.Errorf("tools: plugin %s StartPlugin: %w", id, err)
		}
	}
	a.plugins[id] = plugin
	return nil
}

// RegisterAll 批量注册：先校验所有 plugin 的依赖（含已注册 + 输入列表内），
// 然后按 DependsOn 拓扑排序，最后逐个 Register。
//
// 失败语义：要么全部注册成功，要么维持调用前状态。
// 实际实现：先用临时 map 暂存，校验 + topo sort 全部通过后才 commit 到 a.plugins。
func (a *ToolsAPI) RegisterAll(plugins []Plugin) error {
	if len(plugins) == 0 {
		return nil
	}
	// 第一步：构造临时 map（输入列表内的 plugin），同时检查重名 / id 非法
	temp := make(map[PluginID]Plugin, len(plugins))
	for _, p := range plugins {
		if p == nil {
			return fmt.Errorf("%w: nil plugin in batch", ErrPluginInvalid)
		}
		id := p.ID()
		if err := ValidatePluginID(id); err != nil {
			return fmt.Errorf("%w: %v", ErrPluginInvalid, err)
		}
		if _, exists := temp[id]; exists {
			return fmt.Errorf("%w: %s", ErrPluginDuplicate, id)
		}
		if _, exists := a.plugins[id]; exists {
			return fmt.Errorf("%w: %s", ErrPluginDuplicate, id)
		}
		temp[id] = p
	}

	// 第二步：校验所有依赖（依赖要么在 temp 内，要么已注册）
	for _, p := range temp {
		for _, dep := range p.DependsOn() {
			if _, ok := temp[dep]; ok {
				continue
			}
			if _, ok := a.plugins[dep]; ok {
				continue
			}
			return fmt.Errorf("%w: %s (needed by %s)", ErrPluginNotFound, dep, p.ID())
		}
	}

	// 第三步：拓扑排序（含环检测）
	order, err := topoSort(temp)
	if err != nil {
		return err
	}

	// 第四步：按拓扑序启动 StatefulPlugin（cfg 传 nil，批量注册不支持 per-plugin 初始配置，
	// 需要初始配置的 plugin 请用 Register(plugin, cfg) 单独注册）；
	// 某个失败则回滚已启动的，batch 整体失败不 commit。
	started := make([]StatefulPlugin, 0, len(order))
	for _, id := range order {
		sp, ok := temp[id].(StatefulPlugin)
		if !ok {
			continue
		}
		if err := sp.StartPlugin(a.ws, nil); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				_ = started[i].StopPlugin()
			}
			return fmt.Errorf("tools: plugin %s StartPlugin: %w", id, err)
		}
		started = append(started, sp)
	}

	// 第五步：commit 到主 registry
	for _, id := range order {
		a.plugins[id] = temp[id]
	}
	return nil
}

// Resolve 用 DFS 拓扑排序返回执行顺序（依赖在前）。
// 已注册的 plugin 必须无环（RegisterAll 在 commit 时已保证；直接 Register 多个时也可能引入环——
// Resolve 仍要兜底检测）。
func (a *ToolsAPI) Resolve() ([]PluginID, error) {
	if len(a.plugins) == 0 {
		return nil, nil
	}
	return topoSort(a.plugins)
}

// topoSort 对 plugin map 做 DFS 拓扑排序。
// 返 (order, nil)：order 是依赖在前、执行在后的切片。
// 返 (nil, *CycleError)：环检测触发，error 含环路径。
func topoSort(plugins map[PluginID]Plugin) ([]PluginID, error) {
	const (
		white = 0 // 未访问
		gray  = 1 // 正在访问（在当前 DFS 路径上）
		black = 2 // 已完成
	)
	color := make(map[PluginID]int, len(plugins))
	var path []PluginID
	var order []PluginID

	var visit func(id PluginID) error
	visit = func(id PluginID) error {
		switch color[id] {
		case black:
			return nil
		case gray:
			// 环：把当前 id 到 path 末尾这段切出来
			for i, p := range path {
				if p == id {
					cycle := append([]PluginID(nil), path[i:]...)
					cycle = append(cycle, id) // 闭合
					return &CycleError{Cycle: cycle}
				}
			}
			return &CycleError{Cycle: []PluginID{id}}
		}
		color[id] = gray
		path = append(path, id)
		for _, dep := range plugins[id].DependsOn() {
			// 依赖不在 plugins map 里说明外部漏校验（Register / RegisterAll 已保证）
			// 这里兜底：跳过缺失依赖（理论上不会到这里）
			if _, ok := plugins[dep]; !ok {
				continue
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		order = append(order, id)
		return nil
	}

	// 按 id 字典序访问，保证 Resolve 返的顺序确定性
	ids := make([]PluginID, 0, len(plugins))
	for id := range plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}
