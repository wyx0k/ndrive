// Package tools 提供 ndrive 的插件注册中心。
//
// v5 设计要点：
//   - 核心是 Plugin 接口 + SimplePluginDef helper；用户写 plugin 时两者选一
//   - PluginID 不带 plugin. 前缀；命名空间建议 store.* / func.* / util.* / 自定义
//   - Tool / Func 是从同一注册表派生的双视图（LLM 视角 vs UI 视角）
//   - Context 接口反向使用容器能力；tools 不 import ndrive（避免循环）
//   - 引擎事件用 `plugin.<id>.<lifecycle>` 前缀（与 LLM 工具名 `<id>` 区分）
//   - 起步 tools/builtin 只提供 util.echo 一个极简示例（opt-in 注册）
package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/permission"
)

// PluginID 全局唯一 plugin ID。
// 约定 "xx.xx.xx..." 点分形式，>= 2 段；命名空间建议 store.* / func.* / util.* / 自定义。
// 注意：v5 明确不带 `plugin.` 前缀——id 本身就是 plugin 的身份，前缀冗余。
//
// 实现：定义为 string 的别名（type alias），用户写 `ID_: "util.echo"` 不需要显式转换。
type PluginID = string

// ValidatePluginID 校验 plugin id 格式。
// 规则：>= 2 段点分，每段非空。
// 注：Register 时会自动调用；这是给"想自己提前校验"的人用的公开入口。
func ValidatePluginID(id PluginID) error {
	if id == "" {
		return errors.New("tools: plugin id is empty")
	}
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return fmt.Errorf("tools: plugin id %q need >= 2 dot-separated segments", id)
	}
	for i, p := range parts {
		if p == "" {
			return fmt.Errorf("tools: plugin id %q has empty segment at index %d", id, i)
		}
	}
	return nil
}

// ToolsAPI 是插件注册中心。
// 完整方法（Register / Resolve / Call / Tools / Funcs）在 Spec 4 / Spec 5 接入；
// 本文件先定义类型 + 构造器 + 内部状态，让 Context.API() 的引用能解析。
type ToolsAPI struct {
	ws Context
	// 已注册 plugin；具体字段在 Spec 4 实现 Register 时补全
	plugins map[PluginID]Plugin
}

// NewToolsAPI 构造注册中心。
// Spec 4 会在此基础上加 Register / Resolve；当前只暴露 Context 注入。
func NewToolsAPI(ws Context) *ToolsAPI {
	return &ToolsAPI{
		ws:      ws,
		plugins: make(map[PluginID]Plugin),
	}
}

// Ws 返回构造时注入的 workspace context。
// Spec 4 内部实现会用，外部一般不直接调。
func (a *ToolsAPI) Ws() Context { return a.ws }

// Plugins 返回当前已注册的 plugin 列表（只读快照）。
// Spec 4 Register 后会调用此方法；Spec 3 时为占位。
func (a *ToolsAPI) Plugins() map[PluginID]Plugin { return a.plugins }

// Context 是 tools 包对工作区所需的最小接口。
// 由 ndrive.Workspace 实现；tools 不反向 import ndrive（避免循环）。
// plugin.Call 通过 ws 拿 bus / checker / ToolsAPI 引用。
type Context interface {
	Bus() bus.Bus
	Checker() permission.Checker
	API() *ToolsAPI
}

// Plugin 是所有 plugin 必须实现的接口。
type Plugin interface {
	// ID 全局唯一 plugin 标识。
	ID() PluginID

	// Description 人类可读的描述（LLM 决策用）。
	Description() string

	// Schema JSON Schema，描述 args 结构（LLM 生成参数用）。
	Schema() map[string]any

	// DependsOn 声明本 plugin 依赖的其他 plugin ID。
	// Register 时所有 DependsOn 必须已注册（fail-fast，不支持 forward ref）。
	DependsOn() []PluginID

	// Read true 表示本 plugin 是只读操作（不触发 permission 询问）。
	Read() bool

	// Call 实际执行逻辑。
	// ctx 是调用方 context；ws 是 workspace context；args 由 LLM/UI 构造。
	Call(ctx context.Context, ws Context, args any) (any, error)
}

// SimplePluginDef 是声明式描述 plugin 的 helper struct。
// 让用户免手写 6 个 getter，直接当 struct 用。
// 字段名加下划线后缀（ID_ / Description_ / ...）以避免和 Plugin 接口的 getter 冲突。
type SimplePluginDef struct {
	ID_          PluginID
	Description_ string
	Schema_      map[string]any
	DependsOn_   []PluginID
	Read_        bool
	Fn_          func(ctx context.Context, ws Context, args any) (any, error)
}

// 6 个 getter，自动实现 Plugin 接口

func (s SimplePluginDef) ID() PluginID          { return s.ID_ }
func (s SimplePluginDef) Description() string    { return s.Description_ }
func (s SimplePluginDef) Schema() map[string]any { return s.Schema_ }
func (s SimplePluginDef) DependsOn() []PluginID { return s.DependsOn_ }
func (s SimplePluginDef) Read() bool            { return s.Read_ }
func (s SimplePluginDef) Call(ctx context.Context, ws Context, args any) (any, error) {
	if s.Fn_ == nil {
		return nil, fmt.Errorf("tools: plugin %q has nil Fn_", s.ID_)
	}
	return s.Fn_(ctx, ws, args)
}

// Tool 是 ToolsAPI.Tools() 投影给 LLM 看的视图。
// 字段都是只读快照；不可用于实际调用。
type Tool struct {
	ID          PluginID     `json:"id"`
	Description string       `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Func 是 ToolsAPI.Funcs() 投影给 UI 看的视图。
// Call 已经 bind 了 ws，可以直接调用。
type Func struct {
	ID   PluginID
	Call func(ctx context.Context, args any) (any, error)
}
