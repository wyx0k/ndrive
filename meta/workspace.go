// workspace.go：workspace/<id> key 的 typed facade。
//
// 唯一职责：注册 workspace 或校验所有权（userID 一致性）。
// 持久化形态极简——只存 userID 字符串，无 struct、无 Get/Put。
// 后续要扩展元信息字段时再升级到结构体。
package meta

import (
	"context"
	"fmt"
)

// WorkspaceStore 是 workspace meta 的 typed facade。
// 现状仅暴露 Register；后续按需扩展（Get/List）。
type WorkspaceStore struct {
	store Store
}

// NewWorkspaceStore 构造 facade。
func NewWorkspaceStore(store Store) *WorkspaceStore {
	return &WorkspaceStore{store: store}
}

// Register 注册新 workspace 或校验已存在 workspace 的所有权：
//   - 不存在：记下 userID
//   - 存在：userID 不一致返 ErrWorkspaceExists
//
// 这是 ndrive.OpenWorkspace 的入口逻辑；本包内聚后由本函数承担。
func (s *WorkspaceStore) Register(ctx context.Context, wsID, userID string) error {
	rec, err := s.store.Get(ctx, WorkspaceKey(wsID))
	if err != nil {
		return err
	}
	if rec.Value == nil {
		return s.store.Put(ctx, WorkspaceKey(wsID), []byte(userID))
	}
	if string(rec.Value) != userID {
		return fmt.Errorf("%w: ws=%s want=%s got=%s",
			ErrWorkspaceExists, wsID, string(rec.Value), userID)
	}
	return nil
}
