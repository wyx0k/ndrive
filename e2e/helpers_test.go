// Package e2e 包含 ndrive 作为外部依赖被使用时的端到端测试，
// 按场景拆分到多个文件（workspace / policy / persistence）。
package e2e_test

import (
	"context"
	"testing"

	"github.com/wyx0k/ndrive"
)

func openWS(t *testing.T, id string) *ndrive.Workspace {
	t.Helper()
	ws, err := ndrive.OpenWorkspace(context.Background(), id, "u1", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}
