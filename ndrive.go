// ndrive.go：OpenWorkspace 入口 + 内部装配。
//
// v5 容器层：装配 bus / id / meta / tools / permission 几个子能力。
// 所有持久化 key 全部走 meta 包 facade（meta.NewWorkspaceStore / NewPermissionStore），
// 根包不持有任何 key 字符串。
package ndrive

import (
	"context"
	"errors"
	"fmt"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/id"
	"github.com/wyx0k/ndrive/meta"
	mysqlstore "github.com/wyx0k/ndrive/meta/mysql"
	sqlitestore "github.com/wyx0k/ndrive/meta/sqlite"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

// ErrWorkspaceExists re-exported for external error checking.
// 实际由 meta.ErrWorkspaceExists 包装，errors.Is 仍可识别。
var ErrWorkspaceExists = meta.ErrWorkspaceExists

// OpenWorkspace 创建或打开一个 workspace。
// 流程：init id → 创建 meta store → 持久化 ws meta → 装配 ToolsAPI + Checker → 返 Workspace。
//
// meta store 失败 fail-fast 返 error（不构造半残 ws）。
func OpenWorkspace(ctx context.Context, wsID, userID string, cfg WorkspaceConfig) (*Workspace, error) {
	if wsID == "" {
		return nil, errors.New("ndrive: wsID is empty")
	}
	if userID == "" {
		return nil, errors.New("ndrive: userID is empty")
	}

	// 1. init id 工厂
	if err := id.Init(cfg.WorkerID); err != nil && !errors.Is(err, id.ErrAlreadyInit) {
		return nil, fmt.Errorf("ndrive: id init: %w", err)
	}

	// 2. meta store
	store, err := newMetaStore(ctx, cfg.Meta)
	if err != nil {
		return nil, fmt.Errorf("ndrive: meta store: %w", err)
	}

	// 3. workspace 元信息 facade + register（create-or-check ownership）
	wsStore := meta.NewWorkspaceStore(store)
	if err := wsStore.Register(ctx, wsID, userID); err != nil {
		_ = store.Close()
		return nil, err
	}

	// 4. bus
	b := bus.New()

	// 5. permission facade + checker
	permStore := meta.NewPermissionStore(store)
	checker := permission.NewChecker(permStore, wsID, userID, b)

	// 6. 装配 workspace
	w := &Workspace{
		id:      wsID,
		userID:  userID,
		store:   store,
		bus:     b,
		checker: checker,
		wsStore: wsStore,
		perm:    permStore,
	}
	w.api = tools.NewToolsAPI(w) // 注入 self 引用
	return w, nil
}

// newMetaStore 按 cfg 构造 store。
func newMetaStore(ctx context.Context, cfg MetaConfig) (meta.Store, error) {
	switch c := cfg.(type) {
	case SqliteConfig:
		return sqlitestore.New(ctx, sqlitestore.Config{
			Path:        c.Path,
			MaxOpenConn: c.MaxOpenConn,
		})
	case MysqlConfig:
		return mysqlstore.New(ctx, mysqlstore.Config{
			Host: c.Host, Port: c.Port,
			User: c.User, Password: c.Password, Database: c.Database,
			MaxOpenConns: c.MaxOpenConns, MaxIdleConns: c.MaxIdleConns,
		})
	default:
		return nil, fmt.Errorf("ndrive: unsupported meta config: %T", cfg)
	}
}
