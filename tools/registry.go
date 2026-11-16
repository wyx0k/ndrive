// Registry 实现：Register / RegisterAll / Resolve。
//
// 行为约定：
//   - 注册表是扁平的：每个 tool 一个 id，不存在容器层，tool id 即调用名
//   - DependsOn 是 tool 级别的依赖声明（见 DependentTool）
//   - Register 立即校验 id 合法 + 不重名 + 所有 DependsOn 已注册（fail-fast）
//   - RegisterAll 接收一组 tool，校验 + 拓扑排序后一次性 commit
//   - Resolve 用 DFS 拓扑排序 + 环检测，返回 tool id 的执行顺序（依赖在前）
//   - 错误：ErrToolNotFound / ErrToolDuplicate / ErrToolInvalid / ErrToolCycle
//   - CycleError 是 typed error，Wrap 到 ErrToolCycle
package tools

import (
	"errors"
	"fmt"
	"maps"
	"sort"
)

// 公共错误。
var (
	ErrToolNotFound  = errors.New("tools: tool not found")
	ErrToolDuplicate = errors.New("tools: tool already registered")
	ErrToolInvalid   = errors.New("tools: tool invalid")
	ErrToolCycle     = errors.New("tools: dependency cycle")
)

// CycleError 是 RegisterAll / Resolve 检测到依赖环时的 typed error。
// 用 errors.Is(err, ErrToolCycle) 可统一判定。
type CycleError struct {
	Cycle []ToolID // 环上节点（按顺序，环回到起点）
}

func (e *CycleError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("tools: dependency cycle: %v", e.Cycle)
}

func (e *CycleError) Is(target error) bool {
	return target == ErrToolCycle
}

// dependsOn 返回 tool 声明的依赖（tool id 列表）。
// tool 未实现 DependentTool 时视为没有依赖。
func dependsOn(t Tool) []ToolID {
	d, ok := t.(DependentTool)
	if !ok {
		return nil
	}
	return d.DependsOn()
}

// Register 注册一个 tool。
// 校验顺序：id 合法 → 不重名 → DependsOn（若实现 DependentTool）已注册。
// 任何一步失败，注册表不变（不部分修改）。
func (a *ToolsAPI) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("%w: nil tool", ErrToolInvalid)
	}
	id := t.ID()
	if err := ValidateToolID(id); err != nil {
		return fmt.Errorf("%w: %v", ErrToolInvalid, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.tools[id]; exists {
		return fmt.Errorf("%w: %s", ErrToolDuplicate, id)
	}
	// 依赖必须已注册（forward ref 禁止）
	for _, dep := range dependsOn(t) {
		if _, ok := a.tools[dep]; !ok {
			return fmt.Errorf("%w: %s (needed by %s)", ErrToolNotFound, dep, id)
		}
	}
	a.tools[id] = t
	return nil
}

// RegisterAll 批量注册一组 tool：先校验 id / 重名 / 依赖（含已注册 + 输入列表内），
// 再对依赖图做拓扑排序（含环检测），全部通过后才 commit。
//
// 失败语义：要么全部注册成功，要么维持调用前状态。
// 实际实现：先用临时 map 暂存，校验 + topo sort 全部通过后才 commit 到主注册表。
func (a *ToolsAPI) RegisterAll(ts []Tool) error {
	if len(ts) == 0 {
		return nil
	}
	// 第一步：校验每个 tool 的 id 合法性 + batch 内重名
	temp := make(map[ToolID]Tool, len(ts))
	for _, t := range ts {
		if t == nil {
			return fmt.Errorf("%w: nil tool in batch", ErrToolInvalid)
		}
		id := t.ID()
		if err := ValidateToolID(id); err != nil {
			return fmt.Errorf("%w: %v", ErrToolInvalid, err)
		}
		if _, dup := temp[id]; dup {
			return fmt.Errorf("%w: %s", ErrToolDuplicate, id)
		}
		temp[id] = t
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for id := range temp {
		if _, exists := a.tools[id]; exists {
			return fmt.Errorf("%w: %s", ErrToolDuplicate, id)
		}
	}

	// 第二步：校验所有依赖（依赖要么在 temp 内，要么已注册）
	for id, t := range temp {
		for _, dep := range dependsOn(t) {
			if _, ok := temp[dep]; ok {
				continue
			}
			if _, ok := a.tools[dep]; ok {
				continue
			}
			return fmt.Errorf("%w: %s (needed by %s)", ErrToolNotFound, dep, id)
		}
	}

	// 第三步：按 tool id 拓扑排序（含环检测）
	if _, err := topoSort(temp); err != nil {
		return err
	}

	// 第四步：commit 到主注册表
	maps.Copy(a.tools, temp)
	return nil
}

// Resolve 用 DFS 拓扑排序返回 tool 的执行顺序（依赖在前，按 tool id）。
// 已注册的 tool 必须无环（RegisterAll 在 commit 时已保证；直接 Register 多个时也可能引入环——
// Resolve 仍要兜底检测）。
func (a *ToolsAPI) Resolve() ([]ToolID, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.tools) == 0 {
		return nil, nil
	}
	return topoSort(a.tools)
}

// topoSort 对 toolid -> Tool 的 map 做 DFS 拓扑排序，依赖边来自 dependsOn
// （未实现 DependentTool 的 tool 视为无依赖）。
// 返 (order, nil)：order 是依赖在前、执行在后的切片（tool id）。
// 返 (nil, *CycleError)：环检测触发，error 含环路径。
func topoSort(tools map[ToolID]Tool) ([]ToolID, error) {
	const (
		white = 0 // 未访问
		gray  = 1 // 正在访问（在当前 DFS 路径上）
		black = 2 // 已完成
	)
	color := make(map[ToolID]int, len(tools))
	var path []ToolID
	var order []ToolID

	var visit func(id ToolID) error
	visit = func(id ToolID) error {
		switch color[id] {
		case black:
			return nil
		case gray:
			// 环：把当前 id 到 path 末尾这段切出来
			for i, p := range path {
				if p == id {
					cycle := append([]ToolID(nil), path[i:]...)
					cycle = append(cycle, id) // 闭合
					return &CycleError{Cycle: cycle}
				}
			}
			return &CycleError{Cycle: []ToolID{id}}
		}
		color[id] = gray
		path = append(path, id)
		for _, dep := range dependsOn(tools[id]) {
			// 依赖不在 tools map 里说明外部漏校验（Register / RegisterAll
			// 已保证）这里兜底：跳过缺失依赖（理论上不会到这里）
			if _, ok := tools[dep]; !ok {
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
	ids := make([]ToolID, 0, len(tools))
	for id := range tools {
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
