// Registry 实现：Register / RegisterAll / Resolve。
//
// v5 行为约定：
//   - DependsOn 是容器（Plugin）级别的依赖声明（见 DependentPlugin），不是
//     Tool 级别：一个容器下所有 Tool 共享同一份依赖，依赖图的节点是容器 id
//   - Register 立即校验 id 合法 + 不重名 + 所有 DependsOn（容器 id）已注册（fail-fast）
//   - RegisterAll 接收一组 plugin，按 DependsOn 对容器做拓扑排序后逐个 Register
//   - Resolve 用 DFS 拓扑排序 + 环检测，返回容器 id 的执行顺序（依赖在前）
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
	ErrPluginNoService = errors.New("tools: plugin does not implement ServicePlugin")
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

// fullToolID 拼接容器 id 和 tool 内部 id，得到注册表 / Call 用的完整 id。
// tool.ID() 为空字符串时（singleToolPlugin 包装的裸 Tool），完整 id 就是容器 id 本身。
func fullToolID(containerID PluginID, toolID string) PluginID {
	if toolID == "" {
		return containerID
	}
	return containerID + "." + toolID
}

// asPlugin 把 Register/RegisterAll 的输入统一成 Plugin：已经是 Plugin 就直接用，
// 裸 Tool 自动用 AsPlugin 包装成单 tool 容器。
//
// 同时返回 lifecycleTarget：StatefulPlugin/ReloadablePlugin/ServicePlugin 的
// 接口断言要对它做，而不是对包装后合成出来的容器做——裸 Tool 场景下
// lifecycleTarget 就是原始输入值本身（手写类型可以把 StartPlugin 等方法直接
// 挂在 tool 这层，和旧版本写法一致）；已经是 Plugin 的场景下两者相同。
func asPlugin(v any) (plugin Plugin, lifecycleTarget any, err error) {
	switch x := v.(type) {
	case nil:
		return nil, nil, fmt.Errorf("%w: nil plugin", ErrPluginInvalid)
	case Plugin:
		return x, x, nil
	case Tool:
		return AsPlugin(x), x, nil
	default:
		return nil, nil, fmt.Errorf("%w: %T is neither Plugin nor Tool", ErrPluginInvalid, v)
	}
}

// hasStartStop / hasUpdateConfig / hasService 是 StatefulPlugin /
// ReloadablePlugin / ServicePlugin 去掉 Plugin 嵌入后的"裸方法集"，专门给
// lifecycleTarget 断言用——裸 Tool 场景下 lifecycleTarget 不满足 Plugin，
// 没法直接断言成 StatefulPlugin 等组合接口。
type hasStartStop interface {
	StartPlugin(ws Context, cfg any) error
	StopPlugin() error
}

type hasUpdateConfig interface {
	UpdateConfig(cfg any) error
}

type hasService interface {
	Service() any
}

// containerDependsOn 返回容器声明的依赖（容器 id 列表）。
// plugin 未实现 DependentPlugin 时视为没有依赖。
func containerDependsOn(plugin Plugin) []PluginID {
	dp, ok := plugin.(DependentPlugin)
	if !ok {
		return nil
	}
	return dp.DependsOn()
}

// containerTools 展开一个容器的所有 tool，校验 id 合法性，返回 完整id -> Tool。
func containerTools(plugin Plugin) (map[PluginID]Tool, error) {
	containerID := plugin.ID()
	if err := ValidatePluginID(containerID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPluginInvalid, err)
	}
	tools := plugin.Tools()
	if len(tools) == 0 {
		return nil, fmt.Errorf("%w: plugin %s has no tools", ErrPluginInvalid, containerID)
	}
	out := make(map[PluginID]Tool, len(tools))
	for _, t := range tools {
		full := fullToolID(containerID, t.ID())
		if err := ValidatePluginID(full); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPluginInvalid, err)
		}
		if _, dup := out[full]; dup {
			return nil, fmt.Errorf("%w: %s", ErrPluginDuplicate, full)
		}
		out[full] = t
	}
	return out, nil
}

// Register 注册一个 plugin（容器）或裸 Tool（自动包装成单 tool 容器）。
// 校验顺序：容器 id + 各 tool 完整 id 合法 → 不重名 → 容器的 DependsOn（若实现
// DependentPlugin）已注册。
// 若 plugin 实现了 StatefulPlugin，校验通过后用 cfg 作为初始配置调 StartPlugin
// （不需要初始配置传 nil）。失败时 registry 不变（不部分修改）。
func (a *ToolsAPI) Register(v any, cfg any) error {
	plugin, lifecycleTarget, err := asPlugin(v)
	if err != nil {
		return err
	}
	containerID := plugin.ID()
	newTools, err := containerTools(plugin)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.containers[containerID]; exists {
		return fmt.Errorf("%w: %s", ErrPluginDuplicate, containerID)
	}
	for full := range newTools {
		if _, exists := a.tools[full]; exists {
			return fmt.Errorf("%w: %s", ErrPluginDuplicate, full)
		}
	}
	// 容器依赖必须已注册（forward ref 禁止；Spec 3 walkthrough 决定 C）
	for _, dep := range containerDependsOn(plugin) {
		if _, ok := a.containers[dep]; !ok {
			return fmt.Errorf("%w: %s (needed by %s)", ErrPluginNotFound, dep, containerID)
		}
	}
	if sp, ok := lifecycleTarget.(hasStartStop); ok {
		if err := sp.StartPlugin(a.ws, cfg); err != nil {
			return fmt.Errorf("tools: plugin %s StartPlugin: %w", containerID, err)
		}
	}
	a.containers[containerID] = plugin
	a.lifecycleTargets[containerID] = lifecycleTarget
	for full, t := range newTools {
		a.tools[full] = t
	}
	return nil
}

// RegisterAll 批量注册一组 plugin（容器）或裸 Tool（自动包装）：先校验所有
// 容器的依赖（含已注册 + 输入列表内），然后按 DependsOn 对容器拓扑排序，最后
// 按序启动 StatefulPlugin 并 commit。
//
// 失败语义：要么全部注册成功，要么维持调用前状态。
// 实际实现：先用临时 map 暂存，校验 + topo sort 全部通过后才 commit 到主 registry。
func (a *ToolsAPI) RegisterAll(plugins []Plugin) error {
	if len(plugins) == 0 {
		return nil
	}
	// 第一步：展开每个容器的 tool，构造临时 map（完整id -> Tool），同时检查
	// 容器 id / tool 完整 id 合法性 + batch 内重名
	tempContainers := make(map[PluginID]Plugin, len(plugins))
	tempTools := make(map[PluginID]Tool)
	for _, p := range plugins {
		if p == nil {
			return fmt.Errorf("%w: nil plugin in batch", ErrPluginInvalid)
		}
		containerID := p.ID()
		if _, exists := tempContainers[containerID]; exists {
			return fmt.Errorf("%w: %s", ErrPluginDuplicate, containerID)
		}
		newTools, err := containerTools(p)
		if err != nil {
			return err
		}
		for full := range newTools {
			if _, exists := tempTools[full]; exists {
				return fmt.Errorf("%w: %s", ErrPluginDuplicate, full)
			}
		}
		tempContainers[containerID] = p
		for full, t := range newTools {
			tempTools[full] = t
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for id := range tempContainers {
		if _, exists := a.containers[id]; exists {
			return fmt.Errorf("%w: %s", ErrPluginDuplicate, id)
		}
	}
	for full := range tempTools {
		if _, exists := a.tools[full]; exists {
			return fmt.Errorf("%w: %s", ErrPluginDuplicate, full)
		}
	}

	// 第二步：校验所有容器依赖（依赖要么在 tempContainers 内，要么已注册）
	for cid, p := range tempContainers {
		for _, dep := range containerDependsOn(p) {
			if _, ok := tempContainers[dep]; ok {
				continue
			}
			if _, ok := a.containers[dep]; ok {
				continue
			}
			return fmt.Errorf("%w: %s (needed by %s)", ErrPluginNotFound, dep, cid)
		}
	}

	// 第三步：按容器 id 拓扑排序（含环检测）
	containerOrder, err := topoSortContainers(tempContainers)
	if err != nil {
		return err
	}

	// 第四步：按拓扑顺序启动 StatefulPlugin（cfg 传 nil，批量注册不支持
	// per-plugin 初始配置，需要初始配置的 plugin 请用 Register(plugin, cfg)
	// 单独注册）；某个失败则回滚已启动的，batch 整体失败不 commit。
	started := make([]hasStartStop, 0, len(containerOrder))
	for _, cid := range containerOrder {
		sp, ok := tempContainers[cid].(hasStartStop)
		if !ok {
			continue
		}
		if err := sp.StartPlugin(a.ws, nil); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				_ = started[i].StopPlugin()
			}
			return fmt.Errorf("tools: plugin %s StartPlugin: %w", cid, err)
		}
		started = append(started, sp)
	}

	// 第五步：commit 到主 registry
	for cid, p := range tempContainers {
		a.containers[cid] = p
		a.lifecycleTargets[cid] = p
	}
	for full, t := range tempTools {
		a.tools[full] = t
	}
	return nil
}

// Resolve 用 DFS 拓扑排序返回容器的执行顺序（依赖在前，按容器 id）。
// 已注册的容器必须无环（RegisterAll 在 commit 时已保证；直接 Register 多个时也可能引入环——
// Resolve 仍要兜底检测）。
func (a *ToolsAPI) Resolve() ([]PluginID, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.containers) == 0 {
		return nil, nil
	}
	return topoSortContainers(a.containers)
}

// topoSortContainers 对容器id -> Plugin 的 map 做 DFS 拓扑排序，依赖边来自
// containerDependsOn（未实现 DependentPlugin 的容器视为无依赖）。
// 返 (order, nil)：order 是依赖在前、执行在后的切片（容器 id）。
// 返 (nil, *CycleError)：环检测触发，error 含环路径。
func topoSortContainers(containers map[PluginID]Plugin) ([]PluginID, error) {
	const (
		white = 0 // 未访问
		gray  = 1 // 正在访问（在当前 DFS 路径上）
		black = 2 // 已完成
	)
	color := make(map[PluginID]int, len(containers))
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
		for _, dep := range containerDependsOn(containers[id]) {
			// 依赖不在 containers map 里说明外部漏校验（Register / RegisterAll
			// 已保证）这里兜底：跳过缺失依赖（理论上不会到这里）
			if _, ok := containers[dep]; !ok {
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
	ids := make([]PluginID, 0, len(containers))
	for id := range containers {
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
