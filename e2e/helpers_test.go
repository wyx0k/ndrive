// Package e2e 包含 ndrive 作为外部依赖被使用时的端到端测试，
// 按场景拆分到多个文件（workspace / policy / persistence）。
package e2e_test

import (
	"context"
	"testing"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/fs/local"
	"github.com/wyx0k/ndrive/meta/sqlite"
)

func openWS(t *testing.T, id string) *ndrive.Workspace {
	t.Helper()
	ws, err := ndrive.OpenWorkspace(context.Background(), id, "u1", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: ":memory:"},
		FS:   local.Config{Path: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}
