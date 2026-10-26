// Package tools 提供 ndrive 的插件注册中心。
//
// v5 设计要点：
//   - Plugin 是容器（命名空间 + 一组 Tool），Tool 才是真正的可调用单元；
//     一个 Plugin 可以包含多个 Tool，彼此共享 Plugin 内部状态（如 localfs
//     的 *FS 实例、文件监听 watcher）
//   - 注册到 ToolsAPI 的完整 id = "<Plugin.ID()>.<Tool.ID()>"；registry 内部
//     仍是扁平 map[PluginID]Tool，Call / Resolve / 权限校验按完整 id 操作
//   - Tool.Internal() == true 的 tool 不出现在 Tools()/Funcs()（不暴露给
//     LLM/UI），但仍可被其他 plugin 通过 ToolsAPI.Call(完整id, args) 调用——
//     这是"工具级别"可见性开关，默认 false（导出），不写就保持现状行为
//   - StatefulPlugin / ReloadablePlugin / ServicePlugin 是 Plugin 级别的可选
//     接口（不是 Tool 级别）：一个 Plugin 下所有 Tool 共享同一份启停生命周期 /
//     热更新配置 / 原生 Service
//   - PluginID 不带 plugin. 前缀；命名空间建议 store.* / func.* / util.* / 自定义
//   - ToolInfo（投影类型，原名 Tool，让位给新的 Tool 接口）/ Func 是从同一
//     注册表派生的双视图（LLM 视角 vs UI 视角）
//   - Context 接口反向使用容器能力；tools 不 import ndrive（避免循环）
//   - 引擎事件用 `plugin.<完整id>.<lifecycle>` 前缀（与 LLM 工具名 `<完整id>` 区分）
//   - 起步 tools/builtin 只提供 util.echo 一个极简示例（opt-in 注册）
package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

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
//
// 内部维护三张表：tools 是扁平表，key 是完整 id（"<容器id>.<tool id>"，单
// tool 容器时就是容器 id 本身），Call / Resolve / topoSort / 权限校验都按
// 完整 id 操作；containers 是容器表，key 是容器 id，Tools() 展开用；
// lifecycleTargets 也按容器 id 存，StartPlugin/StopPlugin/UpdateConfig/
// Service 实际做接口断言的对象——真正的 Plugin 容器时就是容器本身，裸 Tool
// 自动包装场景下是原始 Tool 值本身（见 registry.go 的 asPlugin）。
type ToolsAPI struct {
	ws Context
	// mu 保护 tools / containers / lifecycleTargets map；Register /
	// RegisterAll / Call / UpdatePluginConfig / StopAllPlugins / Tools /
	// Funcs / Resolve / Plugins 并发调用时都要拿锁。
	mu                sync.RWMutex
	tools             map[PluginID]Tool
	containers        map[PluginID]Plugin
	lifecycleTargets  map[PluginID]any
}

// NewToolsAPI 构造注册中心。
func NewToolsAPI(ws Context) *ToolsAPI {
	return &ToolsAPI{
		ws:               ws,
		tools:            make(map[PluginID]Tool),
		containers:       make(map[PluginID]Plugin),
		lifecycleTargets: make(map[PluginID]any),
	}
}

// Ws 返回构造时注入的 workspace context。
func (a *ToolsAPI) Ws() Context { return a.ws }

// Plugins 返回当前已注册 tool 的只读快照（完整id -> Tool，拷贝，非内部 map 引用）。
func (a *ToolsAPI) Plugins() map[PluginID]Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[PluginID]Tool, len(a.tools))
	for id, t := range a.tools {
		out[id] = t
	}
	return out
}

// StopAllPlugins 对所有已注册的 StatefulPlugin 容器调 StopPlugin。
// 由 Workspace.Close 调用；单个 plugin 的错误会被收集但不阻止其余 plugin 停止。
func (a *ToolsAPI) StopAllPlugins() error {
	a.mu.RLock()
	targets := make(map[PluginID]any, len(a.lifecycleTargets))
	for id, t := range a.lifecycleTargets {
		targets[id] = t
	}
	a.mu.RUnlock()

	var errs []error
	for id, t := range targets {
		sp, ok := t.(hasStartStop)
		if !ok {
			continue
		}
		if err := sp.StopPlugin(); err != nil {
			errs = append(errs, fmt.Errorf("tools: plugin %s StopPlugin: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Context 是 tools 包对工作区所需的最小接口。
// 由 ndrive.Workspace 实现；tools 不反向 import ndrive（避免循环）。
// Tool.Call 通过 ws 拿 bus / checker / ToolsAPI 引用。
type Context interface {
	Bus() bus.Bus
	Checker() permission.Checker
	API() *ToolsAPI
}

// Tool 是注册中心里真正的可调用单元（一次 Call 对应一个 Tool）。
// 一个 Plugin 可以包含多个 Tool，彼此共享 Plugin 内部状态。
type Tool interface {
	// ID 在所属 Plugin 内部唯一（不是全局唯一）；注册后的完整 id 是
	// "<Plugin.ID()>.<Tool.ID()>"，由 ToolsAPI 在 Register 时拼接。
	ID() string

	// Description 人类可读的描述（LLM 决策用）。
	Description() string

	// Schema JSON Schema，描述 args 结构（LLM 生成参数用）。
	Schema() map[string]any

	// DependsOn 声明本 tool 依赖的其他已注册 tool 的完整 id。
	// Register 时所有 DependsOn 必须已注册（fail-fast，不支持 forward ref）。
	DependsOn() []PluginID

	// Read true 表示本 tool 是只读操作（不触发 permission 询问）。
	Read() bool

	// Internal true 表示本 tool 不出现在 Tools()/Funcs()（不暴露给 LLM/UI），
	// 但仍可被其他 plugin 通过 ToolsAPI.Call(完整id, args) 调用——用于"只给
	// 其他 plugin 用，不对外暴露"的内部工具（如 localfs 的 open）。
	// 默认 false（导出），不设置时保持和旧版本一致的可见行为。
	Internal() bool

	// Call 实际执行逻辑。
	// ctx 是调用方 context；ws 是 workspace context；args 由 LLM/UI 构造。
	Call(ctx context.Context, ws Context, args any) (any, error)
}

// Plugin 是插件容器：一个命名空间下的一组 Tool，彼此可以共享内部状态
// （如 localfs 的 *FS 实例、文件监听 watcher）。
//
// 单个独立能力（没有兄弟 tool、不需要共享状态）可以直接用 SimplePluginDef
// 注册——ToolsAPI.Register/RegisterAll 接受裸 Tool，会自动包装成一个只含
// 该 tool 的容器，容器 id 等于 tool 的完整 id，用法和旧版本完全一致。
type Plugin interface {
	// ID 全局唯一的插件命名空间标识。
	ID() PluginID

	// Tools 返回本插件包含的所有 Tool。
	Tools() []Tool
}

// StatefulPlugin 是可选接口，供有内部状态/后台资源的 Plugin（如文件系统监听、
// 长连接）实现，获得启停生命周期钩子。这是 Plugin（容器）级别的接口，不是
// Tool 级别——一个 Plugin 下所有 Tool 共享同一份启停生命周期。
//
// Register(plugin, cfg) 成功校验后会 type-assert 并自动调 StartPlugin(ws, cfg)，
// cfg 就是调用 Register 时传入的初始配置（不需要初始配置传 nil）；
// Workspace.Close（经 ToolsAPI.StopAllPlugins）会对所有已注册的 StatefulPlugin
// 调 StopPlugin。StartPlugin 拿到的 Context 可长期持有，用于在后台
// goroutine 里调 ws.Bus().Publish 主动发事件。
type StatefulPlugin interface {
	Plugin

	// StartPlugin 在 plugin 注册成功后立即调用一次，cfg 是注册时传入的初始配置。
	// 典型实现：按 cfg 建立文件系统监听 / 打开长连接，并保存 ws 供后台 goroutine 使用。
	// 返回 error 时 Register 整体失败（registry 不保留该 plugin）。
	StartPlugin(ws Context, cfg any) error

	// StopPlugin 在 workspace 关闭时调用一次，用于释放
	// StartPlugin 建立的资源（移除文件监听、关闭连接、停止 goroutine）。
	StopPlugin() error
}

// ReloadablePlugin 是可选接口，职责仅限运行中热更新配置：只会被
// ToolsAPI.UpdatePluginConfig 调用，和 Register / StartPlugin 的初始配置流程
// 无关（StartPlugin 的 cfg 参数是独立输入，框架不会用它来调 UpdateConfig）。
// 这是 Plugin（容器）级别的接口：一次 UpdateConfig 影响整个 Plugin 下所有 Tool
// 共享的状态。若 plugin 想让初始配置和热更新复用同一套校验/生效逻辑，可在
// StartPlugin 内部自行调用自己的 UpdateConfig；这是 plugin 的实现选择，不是
// 接口约定。
type ReloadablePlugin interface {
	Plugin

	// UpdateConfig 在 plugin 运行期间更新配置，由 ToolsAPI.UpdatePluginConfig 触发。
	// cfg 的具体结构由 plugin 自己定义；plugin 必须自行做类型断言和校验，
	// 无效配置应返回 error 且不生效（保留更新前的状态）。
	UpdateConfig(cfg any) error
}

// ServicePlugin 是可选接口，供 Plugin 向其他 Plugin 暴露一个原生 Go 能力
// （而不是走 Call 的 schema/permission/事件通道）。这是 Plugin（容器）级别的
// 接口：一个 Plugin 下所有 Tool 共享同一份原生能力（通常就是 Tool 内部实际
// 调用的那个具体实现，如 localfs 的 *FS）。
//
// 典型场景：一个 plugin 提供基础能力（如文件读写），另一个 plugin 需要高频/
// 流式使用这个能力（如按 io.ReadCloser 打开文件交给 zip 解析器），
// 不适合每次都走 ToolsAPI.Call 的 any 参数 + 权限门控 + 事件广播。
//
// 用法：
//   - 提供方在 Service() 里返回一个接口值（通常是提供方和消费方都认可的
//     标准接口，如 tools/builtin.FileService）
//   - 消费方在自己的 DependsOn_ 里声明依赖该 plugin id，Register 阶段保证
//     依赖已注册；运行时用 ws.API().Service(容器id) 取出后做类型断言
//   - 这是 Go 原生类型/接口的协作通道，调用方自己负责资源释放（如 defer Close()）
type ServicePlugin interface {
	Plugin

	// Service 返回本 plugin 对外提供的原生能力，通常是一个接口值。
	Service() any
}

// SimplePluginDef 是声明式描述单个 Tool 的 helper struct。
// 让用户免手写 7 个 getter，直接当 struct 用。
// 字段名加下划线后缀（ID_ / Description_ / ...）以避免和 Tool 接口的 getter 冲突。
//
// SimplePluginDef 同时实现 Tool 和 Plugin 两个接口：作为 Plugin，它是一个只
// 含自己这一个 tool 的容器，容器 id 就是 ID_；作为 Tool，它是容器内那一个
// tool。这样 `ws.Register(tools.SimplePluginDef{ID_: "util.echo", ...}, nil)`
// 和 `[]tools.Plugin{tools.SimplePluginDef{...}, ...}` 两种写法都和旧版本
// 完全一致，不需要改动调用方。
type SimplePluginDef struct {
	ID_          PluginID
	Description_ string
	Schema_      map[string]any
	DependsOn_   []PluginID
	Read_        bool
	// Internal_ true 表示不出现在 Tools()/Funcs()，但仍可被 Call。默认 false（导出）。
	Internal_ bool
	Fn_       func(ctx context.Context, ws Context, args any) (any, error)
}

// 7 个 getter，自动实现 Tool 接口

func (s SimplePluginDef) ID() PluginID           { return s.ID_ }
func (s SimplePluginDef) Description() string    { return s.Description_ }
func (s SimplePluginDef) Schema() map[string]any { return s.Schema_ }
func (s SimplePluginDef) DependsOn() []PluginID  { return s.DependsOn_ }
func (s SimplePluginDef) Read() bool             { return s.Read_ }
func (s SimplePluginDef) Internal() bool         { return s.Internal_ }
func (s SimplePluginDef) Call(ctx context.Context, ws Context, args any) (any, error) {
	if s.Fn_ == nil {
		return nil, fmt.Errorf("tools: plugin %q has nil Fn_", s.ID_)
	}
	return s.Fn_(ctx, ws, args)
}

// Tools 让 SimplePluginDef 同时满足 Plugin 接口：返回只含自己的单 tool 列表，
// tool 侧 ID() 返回空字符串，于是完整 id = 容器 id = s.ID_，不做额外拼接。
func (s SimplePluginDef) Tools() []Tool {
	return []Tool{&bareIDTool{Tool: s}}
}

// singleToolPlugin 把一个裸 Tool 包装成只含它自己的单 tool Plugin 容器。
// 容器 id 直接复用 tool 的 ID()（通常已经是点分完整 id，如 "util.echo"），
// 这样注册后的完整 id（容器id + "." + tool.ID()）会和旧版本行为不一致——
// 所以这里特殊处理：单 tool 容器的 Tools() 里，tool 的 ID() 返回空字符串，
// 由 ToolsAPI 识别空 tool id 为"容器自身即完整 id"，不做拼接。
//
// 注意：singleToolPlugin 本身不实现 StatefulPlugin/ReloadablePlugin/
// ServicePlugin——如果原始 tool 实现了 StartPlugin/StopPlugin/UpdateConfig/
// Service（手写类型按旧版本习惯把这些方法挂在"tool"这层），registry 会在
// 包装前先对原始 tool 做一次接口断言（见 registry.go 的 lifecycleTarget），
// 而不是对这个合成出来的容器断言。
type singleToolPlugin struct {
	id   PluginID
	tool Tool
}

func (p *singleToolPlugin) ID() PluginID { return p.id }
func (p *singleToolPlugin) Tools() []Tool {
	return []Tool{&bareIDTool{Tool: p.tool}}
}

// bareIDTool 包装一个 Tool，让它在容器内的 ID() 返回空字符串，标记"不拼接 id"。
type bareIDTool struct {
	Tool
}

func (t *bareIDTool) ID() string { return "" }

// AsPlugin 把一个裸 Tool 包装成单 tool Plugin 容器，容器 id = tool.ID()。
// Register/RegisterAll 在收到非 Plugin 的 Tool 时会自动调用它；一般不需要
// 用户手动调用。
func AsPlugin(t Tool) Plugin {
	return &singleToolPlugin{id: t.ID(), tool: t}
}

// ToolInfo 是 ToolsAPI.Tools() 投影给 LLM 看的视图。
// 字段都是只读快照；不可用于实际调用。
type ToolInfo struct {
	ID          PluginID       `json:"id"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Func 是 ToolsAPI.Funcs() 投影给 UI 看的视图。
// Call 已经 bind 了 ws，可以直接调用。
type Func struct {
	ID   PluginID
	Call func(ctx context.Context, args any) (any, error)
}
