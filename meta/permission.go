// permission.go：permission_binding/<wsID>/<userID> key 的 typed facade。
//
// 实现 permission.WorkspaceStore 接口；这样 permission 包本身不知道 meta 存在，
// meta 知道 permission（单向依赖，无循环）。
package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wyx0k/ndrive/permission"
)

// PermissionStore 是 permission meta 的 typed facade。
// 直接实现 permission.WorkspaceStore 接口。
type PermissionStore struct {
	store Store
}

// NewPermissionStore 构造 facade。
func NewPermissionStore(store Store) *PermissionStore {
	return &PermissionStore{store: store}
}

// GetBinding 实现 permission.WorkspaceStore。
// 不存在返 ErrNotFound（包装形式，errors.Is 可识别）。
func (s *PermissionStore) GetBinding(ctx context.Context, wsID, userID string) (permission.Binding, error) {
	rec, err := s.store.Get(ctx, PermissionKey(wsID, userID))
	if err != nil {
		return permission.Binding{}, err
	}
	if rec.Value == nil {
		return permission.Binding{}, fmt.Errorf("%w: binding ws=%s user=%s", ErrNotFound, wsID, userID)
	}
	var b permission.Binding
	if err := json.Unmarshal(rec.Value, &b); err != nil {
		return permission.Binding{}, err
	}
	return b, nil
}

// SetBinding 实现 permission.WorkspaceStore。
func (s *PermissionStore) SetBinding(ctx context.Context, b permission.Binding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, PermissionKey(b.WorkspaceID, b.UserID), data)
}

// DeleteBinding 实现 permission.WorkspaceStore。
func (s *PermissionStore) DeleteBinding(ctx context.Context, wsID, userID string) error {
	return s.store.Delete(ctx, PermissionKey(wsID, userID))
}

// ListBindings 实现 permission.WorkspaceStore。
func (s *PermissionStore) ListBindings(ctx context.Context, wsID string) ([]permission.Binding, error) {
	recs, err := s.store.List(ctx, PermissionPrefix(wsID))
	if err != nil {
		return nil, err
	}
	out := make([]permission.Binding, 0, len(recs))
	for _, r := range recs {
		var b permission.Binding
		if err := json.Unmarshal(r.Value, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// 编译期断言：PermissionStore 实现 permission.WorkspaceStore。
var _ permission.WorkspaceStore = (*PermissionStore)(nil)

// 引用 errors 以防未用到（errors 实际在 GetBinding 用 fmt.Errorf %w）
var _ = errors.New
