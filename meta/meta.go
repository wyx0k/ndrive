// Package meta 提供 ndrive 的持久化抽象 + 所有 meta 数据的统一管理。
//
// 内聚设计：本包是 ndrive meta 存储的"唯一真源"，任何持久化 key 都从这里导出。
// 上层（根 ndrive 包）不直接拼 key 字符串，全部走本包的 typed facade。
//
// 三个 facade：
//   - WorkspaceStore    workspace/<id>                  元信息（id + userID）
//   - PermissionStore   permission_binding/<wsID>/<userID>  权限绑定
//   - （未来）PluginStore  plugin/<id>                    描述符
//
// 底层：
//   - Store interface 通用 KV（实现方：sqlite / mysql）
//   - keys.go 所有 key 命名规则
package meta

import (
	"context"
	"errors"
)

// Record 是 Store 的基本读写单位。
type Record struct {
	Key   string
	Value []byte
}

// Store meta 持久化接口。实现方：sqlite / mysql。
type Store interface {
	Get(ctx context.Context, key string) (Record, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Record, error)
	Close() error
}

// 公共错误。
var (
	// ErrNotFound key 不存在（Get 返空值时）。
	ErrNotFound = errors.New("meta: not found")

	// ErrWorkspaceExists 已存在同 ID 的 workspace（userID 不一致时）。
	ErrWorkspaceExists = errors.New("meta: workspace exists with different userID")
)
