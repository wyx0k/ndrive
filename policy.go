// policy.go：Workspace 上的 permission 管理方法。
// v5：SetPolicy / GetPolicy / ListPolicies / DeletePolicy 按 userID 操作，
// 持久化到 permission_binding/<wsID>/<userID> key。
package ndrive

import (
	"context"
	"errors"

	"github.com/wyx0k/ndrive/permission"
)

// SetPolicy 设置指定 userID 在本 workspace 的权限 mode。
// userID 可以是任意用户（不限 owner）；同 (wsID, userID) 二次设置覆盖。
func (w *Workspace) SetPolicy(ctx context.Context, userID string, mode permission.Mode) error {
	if userID == "" {
		return errors.New("ndrive: SetPolicy userID is empty")
	}
	return w.perm.SetBinding(ctx, permission.Binding{
		WorkspaceID: w.id,
		UserID:      userID,
		Mode:        mode,
	})
}

// GetPolicy 获取指定 userID 的权限 mode。
// userID 不存在返 error（permission 包内有 "binding not found" 错误）。
func (w *Workspace) GetPolicy(ctx context.Context, userID string) (permission.Mode, error) {
	if userID == "" {
		return 0, errors.New("ndrive: GetPolicy userID is empty")
	}
	b, err := w.perm.GetBinding(ctx, w.id, userID)
	if err != nil {
		return 0, err
	}
	return b.Mode, nil
}

// ListPolicies 列出本 workspace 下所有 binding。
func (w *Workspace) ListPolicies(ctx context.Context) ([]permission.Binding, error) {
	return w.perm.ListBindings(ctx, w.id)
}

// DeletePolicy 删除指定 userID 的 binding。
func (w *Workspace) DeletePolicy(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("ndrive: DeletePolicy userID is empty")
	}
	if err := w.perm.DeleteBinding(ctx, w.id, userID); err != nil {
		return err
	}
	return nil
}
