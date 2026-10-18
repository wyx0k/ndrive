// workspace.go：Workspace struct + 公共方法。
package ndrive

import (
	"context"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/meta"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

// Workspace 工作区句柄（v5 容器层主类型）。
// 实现 tools.Context 接口（让 ToolsAPI.Call 能注入到 plugin）。
type Workspace struct {
	id      string
	userID  string
	store   meta.Store
	bus     bus.Bus
	api     *tools.ToolsAPI
	checker permission.Checker
	wsStore *meta.WorkspaceStore
	perm    *meta.PermissionStore
}

// ID 返 workspace 标识。
func (w *Workspace) ID() string { return w.id }

// UserID 返当前用户标识。
func (w *Workspace) UserID() string { return w.userID }

// Bus 返进程内事件总线。
func (w *Workspace) Bus() bus.Bus { return w.bus }

// API 返 ToolsAPI（plugin 注册中心）。
func (w *Workspace) API() *tools.ToolsAPI { return w.api }

// Checker 返 permission 检查器。
// 满足 tools.Context 接口（被 ToolsAPI 用于门控）。
func (w *Workspace) Checker() permission.Checker { return w.checker }

// Context 把 wsID + userID 注入到 ctx，供下游 plugin / handler 用。
func (w *Workspace) Context(ctx context.Context) context.Context {
	return withWS(ctx, w.id, w.userID)
}

// Register 是 ws.API().Register 的便捷包装。
func (w *Workspace) Register(p tools.Plugin) error {
	return w.api.Register(p)
}

// Close 关闭 meta store。
// 注：id 工厂无状态，不需关闭；bus 无 Close。
func (w *Workspace) Close() error {
	if w.store != nil {
		return w.store.Close()
	}
	return nil
}

// --- ctx keys ---

type wsCtxKey struct{}

type wsCtxValue struct {
	wsID   string
	userID string
}

func withWS(ctx context.Context, wsID, userID string) context.Context {
	return context.WithValue(ctx, wsCtxKey{}, wsCtxValue{wsID: wsID, userID: userID})
}

// FromContext 从 ctx 提取 (wsID, userID)；缺失时返空串。
func FromContext(ctx context.Context) (wsID, userID string) {
	v, ok := ctx.Value(wsCtxKey{}).(wsCtxValue)
	if !ok {
		return "", ""
	}
	return v.wsID, v.userID
}
