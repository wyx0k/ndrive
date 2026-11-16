// Package tools 提供 ndrive 的工具注册中心。
//
// 设计要点：
//   - Tool 是唯一的可调用单元：id + description + schema + read 标志 + 执行体。
//     没有"插件容器"这一层——注册表就是一张扁平的 map[ToolID]Tool
//   - Call 是唯一的执行入口：权限门控 + 生命周期事件都在这一层，AI 调用和
//     直接函数调用走的是同一条路径
//   - Tools() 是 LLM 视角投影（id + description + json schema），供生成
//     tool 定义；只有这一个投影
//   - DependentTool 是 Tool 级别的可选接口：依赖声明挂在单个 tool 上
//   - ToolID 不带 tool. 前缀；命名空间建议 store.* / func.* / util.* / 自定义
//   - 引擎事件用 `tool.<id>.<lifecycle>` 前缀（与 LLM 工具名 `<id>` 区分）
//   - Context 接口反向使用工作区能力；tools 不 import ndrive（避免循环）
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

// ToolID 全局唯一 tool ID。
// 约定 "xx.xx.xx..." 点分形式，>= 2 段；命名空间建议 store.* / func.* / util.* / 自定义。
// 注意：id 本身就是 tool 的身份，前缀冗余——不带 `tool.` 前缀。
//
// 实现：定义为 string 的别名（type alias），用户写 `ID_: "util.echo"` 不需要显式转换。
type ToolID = string

// ValidateToolID 校验 tool id 格式。
// 规则：>= 2 段点分，每段非空。
// 注：Register 时会自动调用；这是给"想自己提前校验"的人用的公开入口。
func ValidateToolID(id ToolID) error {
	if id == "" {
		return errors.New("tools: tool id is empty")
	}
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return fmt.Errorf("tools: tool id %q need >= 2 dot-separated segments", id)
	}
	for i, p := range parts {
		if p == "" {
			return fmt.Errorf("tools: tool id %q has empty segment at index %d", id, i)
		}
	}
	return nil
}

// ToolsAPI 是工具注册中心。
//
// 内部只有一张扁平表 tools，key 是 tool id；Call / Resolve / 拓扑排序 /
// 权限校验都按 tool id 操作。没有容器层，也就没有"容器 id"和"完整 id"的区别。
type ToolsAPI struct {
	ws Context
	// mu 保护 tools map；Register / RegisterAll / Call / Tools /
	// Resolve 并发调用时都要拿锁。
	mu    sync.RWMutex
	tools map[ToolID]Tool
}

// NewToolsAPI 构造注册中心。
func NewToolsAPI(ws Context) *ToolsAPI {
	return &ToolsAPI{
		ws:    ws,
		tools: make(map[ToolID]Tool),
	}
}

// Ws 返回构造时注入的 workspace context。
func (a *ToolsAPI) Ws() Context { return a.ws }

// Context 是 tools 包对工作区所需的最小接口。
// 由 ndrive.Workspace 实现；tools 不反向 import ndrive（避免循环）。
// Tool.Call 通过 ws 拿 bus / checker / ToolsAPI 引用。
type Context interface {
	Bus() bus.Bus
	Checker() permission.Checker
	API() *ToolsAPI
}

// Tool 是注册中心里唯一的可调用单元（一次 Call 对应一个 Tool）。
//
// Tool 的 id 就是注册表里的键，也是 Call 时使用的名字。多个 tool 之间需要
// 共享状态时，由调用方在注册时用闭包捕获同一个实例。
type Tool interface {
	// ID 全局唯一标识；注册后即调用时使用的完整 id。
	ID() ToolID

	// Description 人类可读的描述（LLM 决策用）。
	Description() string

	// Schema JSON Schema，描述 args 结构（LLM 生成参数用）。
	Schema() map[string]any

	// Read true 表示本 tool 是只读操作（不触发 permission 询问）。
	Read() bool

	// Call 实际执行逻辑。
	// ctx 是调用方 context；ws 是 workspace context；args 由 LLM/UI 构造。
	Call(ctx context.Context, ws Context, args any) (any, error)
}

// DependentTool 是可选接口，供需要声明跨 tool 依赖的实现。
//
// Register/RegisterAll 时，若 tool 实现了 DependentTool，会校验 DependsOn()
// 里的每个 tool id 必须已注册（fail-fast，不支持 forward ref）；未实现该接口
// 的 tool 视为没有依赖。RegisterAll 按 tool id 做拓扑排序 + 环检测。
type DependentTool interface {
	Tool

	// DependsOn 声明本 tool 依赖的其他 tool id。
	DependsOn() []ToolID
}

// ToolDef 是声明式描述单个 Tool 的 helper struct。
// 让用户免手写方法，直接当 struct 用。
// 字段名加下划线后缀（ID_ / Description_ / ...）以避免和 Tool 接口的 getter 冲突。
//
// ToolDef 同时满足 Tool 和 DependentTool：注册时 `ws.Register(tools.ToolDef{ID_:
// "util.echo", ...})` 即可；DependsOn_ 为空表示没有依赖。
type ToolDef struct {
	ID_          ToolID
	Description_ string
	Schema_      map[string]any
	// DependsOn_ 声明本 tool 依赖的其他 tool id。
	DependsOn_ []ToolID
	Read_      bool
	Fn_        func(ctx context.Context, ws Context, args any) (any, error)
}

// 自动实现 Tool 接口的 getter

func (s ToolDef) ID() ToolID             { return s.ID_ }
func (s ToolDef) Description() string    { return s.Description_ }
func (s ToolDef) Schema() map[string]any { return s.Schema_ }
func (s ToolDef) Read() bool             { return s.Read_ }
func (s ToolDef) Call(ctx context.Context, ws Context, args any) (any, error) {
	if s.Fn_ == nil {
		return nil, fmt.Errorf("tools: tool %q has nil Fn_", s.ID_)
	}
	return s.Fn_(ctx, ws, args)
}

// DependsOn 让 ToolDef 满足 DependentTool（依赖声明）。
func (s ToolDef) DependsOn() []ToolID { return s.DependsOn_ }

// ToolInfo 是 ToolsAPI.Tools() 投影给 LLM 看的视图。
// 字段都是只读快照；不可用于实际调用。
type ToolInfo struct {
	ID          ToolID         `json:"id"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}
