// workspace.go：Workspace struct + 公共方法。
package ndrive

import (
	"context"
	"errors"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/fs"
	"github.com/wyx0k/ndrive/meta"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

// Workspace 工作区句柄（容器层主类型）。
// 实现 tools.Context 接口（让 ToolsAPI.Call 能注入到 tool）。
type Workspace struct {
	id      string
	userID  string
	store   meta.Store
	bus     bus.Bus
	api     *tools.ToolsAPI
	checker permission.Checker
	wsStore *meta.WorkspaceStore
	perm    *meta.PermissionStore
	fs      fs.FileService
}

// ID 返 workspace 标识。
func (w *Workspace) ID() string { return w.id }

// UserID 返当前用户标识。
func (w *Workspace) UserID() string { return w.userID }

// Bus 返进程内事件总线。
func (w *Workspace) Bus() bus.Bus { return w.bus }

// API 返 ToolsAPI（tool 注册中心）。
func (w *Workspace) API() *tools.ToolsAPI { return w.api }

// Checker 返 permission 检查器。
// 满足 tools.Context 接口（被 ToolsAPI 用于门控）。
func (w *Workspace) Checker() permission.Checker { return w.checker }

// FS 返工作区文件系统。
// 实现方（fs-local / 未来的 fs-mem / fs-single-file）都通过 fs.FileService 接口
// 暴露，调用方只依赖接口。
func (w *Workspace) FS() fs.FileService { return w.fs }

// Context 把 wsID + userID 注入到 ctx，供下游 tool / handler 用。
func (w *Workspace) Context(ctx context.Context) context.Context {
	return withWS(ctx, w.id, w.userID)
}

// Register 是 ws.API().Register 的便捷包装。
// t 是要注册的可调用单元。
func (w *Workspace) Register(t tools.Tool) error {
	return w.api.Register(t)
}

// Close 关闭文件系统与 meta store。
// 注：bus 无 Close；工具本身没有框架级生命周期（见 tools 包说明）——
// 需要释放资源的 tool 自己管（tool 拿到的 ws.Context 里能拿到 Workspace）。
func (w *Workspace) Close() error {
	var errs []error
	// 文件系统可能持有监听 goroutine（fs-local 的 watcher），能关就关。
	if w.fs != nil {
		if c, ok := w.fs.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if w.store != nil {
		if err := w.store.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
