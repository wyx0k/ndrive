// Package meta 提供 ndrive 的持久化抽象 + 所有 meta 数据的统一管理。
//
// 内聚设计：本包是 ndrive meta 存储的"唯一真源"，任何持久化 key 都从这里导出。
// 上层（根 ndrive 包）不直接拼 key 字符串，全部走本包的 typed facade。
//
// 两个 facade：
//   - WorkspaceStore    workspace/<id>                  元信息（id + userID）
//   - PermissionStore   permission_binding/<wsID>/<userID>  权限绑定
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

// Config 是 meta 存储配置的契约：配置对象负责把自己初始化成 Store。
//
// 实现方（meta/sqlite、meta/mysql 等）提供自己的 Config 类型并实现
// NewStore；根包只持有这个接口，不认识任何具体实现——新增后端不用改根包。
type Config interface {
	// NewStore 按配置初始化出一个 Store。
	NewStore(ctx context.Context) (Store, error)
}

// 公共错误。
var (
	// ErrNotFound key 不存在（Get 返空值时）。
	ErrNotFound = errors.New("meta: not found")

	// ErrWorkspaceExists 已存在同 ID 的 workspace（userID 不一致时）。
	ErrWorkspaceExists = errors.New("meta: workspace exists with different userID")
)
