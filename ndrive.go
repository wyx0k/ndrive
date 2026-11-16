// ndrive.go：OpenWorkspace 入口 + 内部装配。
//
// 装配 bus / meta / tools / permission / fs 几个子能力。
// 所有持久化 key 全部走 meta 包 facade（meta.NewWorkspaceStore /
// NewPermissionStore），根包不持有任何 key 字符串。
//
// 外部依赖只有两个（meta store + 文件系统），都由调用方给配置对象；
// 根包不认识任何具体实现——换后端 = 换配置对象。
package ndrive

import (
	"context"
	"errors"
	"fmt"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/fs"
	"github.com/wyx0k/ndrive/meta"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

// ErrWorkspaceExists re-exported for external error checking.
// 实际由 meta.ErrWorkspaceExists 包装，errors.Is 仍可识别。
var ErrWorkspaceExists = meta.ErrWorkspaceExists

// OpenWorkspace 创建或打开一个 workspace。
// 流程：创建 meta store → 持久化 ws meta → 装配 ToolsAPI + Checker + 文件系统
// → 返 Workspace。
//
// meta store 失败 fail-fast 返 error（不构造半残 ws）。
func OpenWorkspace(ctx context.Context, wsID, userID string, cfg WorkspaceConfig) (*Workspace, error) {
	if wsID == "" {
		return nil, errors.New("ndrive: wsID is empty")
	}
	if userID == "" {
		return nil, errors.New("ndrive: userID is empty")
	}

	// 1. meta store（配置对象自己初始化）
	store, err := newMetaStore(ctx, cfg.Meta)
	if err != nil {
		return nil, fmt.Errorf("ndrive: meta store: %w", err)
	}

	// 2. workspace 元信息 facade + register（create-or-check ownership）
	wsStore := meta.NewWorkspaceStore(store)
	if err := wsStore.Register(ctx, wsID, userID); err != nil {
		_ = store.Close()
		return nil, err
	}

	// 3. bus
	b := bus.New()

	// 4. permission facade + checker
	permStore := meta.NewPermissionStore(store)
	checker := permission.NewChecker(permStore, wsID, userID, b)

	// 5. 工作区文件系统（root 目录 + /.tmp + /.mem 由 fs 实现负责准备）
	fsvc, err := newFileService(ctx, cfg.FS)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	// 6. 文件变更事件 → bus（fs 自己不依赖 bus，由这里桥接）
	bridgeFSEvents(b, fsvc)

	// 7. 装配 workspace
	w := &Workspace{
		id:      wsID,
		userID:  userID,
		store:   store,
		bus:     b,
		checker: checker,
		wsStore: wsStore,
		perm:    permStore,
		fs:      fsvc,
	}
	w.api = tools.NewToolsAPI(w) // 注入 self 引用
	return w, nil
}

// newMetaStore 按 cfg 构造 store。配置对象自己负责初始化，根包不 switch。
func newMetaStore(ctx context.Context, cfg meta.Config) (meta.Store, error) {
	if cfg == nil {
		return nil, errors.New("ndrive: workspace config requires Meta")
	}
	return cfg.NewStore(ctx)
}

// newFileService 按 cfg 构造工作区文件系统，并校验约定目录存在。
//
// 约定目录（/.tmp、/.mem）是每个 fs 实现都必须提供的：fs 实现负责创建，
// 这里再校验一遍——接入自研 fs 实现时，缺约定目录的实现会在这里 fail-fast，
// 而不是等到上层读写时才出问题。
func newFileService(ctx context.Context, cfg fs.Config) (fs.FileService, error) {
	if cfg == nil {
		return nil, errors.New("ndrive: workspace config requires FS")
	}
	svc, err := cfg.NewFileService(ctx)
	if err != nil {
		return nil, fmt.Errorf("ndrive: fs: %w", err)
	}
	for _, dir := range []string{fs.TmpDir, fs.MemDir} {
		if _, err := svc.Stat(ctx, dir); err != nil {
			return nil, fmt.Errorf("ndrive: fs convention dir %q: %w", dir, err)
		}
	}
	return svc, nil
}
