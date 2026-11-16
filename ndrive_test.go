package ndrive_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/fs"
	"github.com/wyx0k/ndrive/fs/local"
	"github.com/wyx0k/ndrive/meta/sqlite"
)

// openWS 在临时目录上开一个 workspace。
func openWS(t *testing.T, root string) *ndrive.Workspace {
	t.Helper()
	ws, err := ndrive.OpenWorkspace(context.Background(), "demo", "u1", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: ":memory:"},
		FS:   local.Config{Path: root},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// TestOpenWorkspaceRequiresFS 验证 FS 是必填配置项：工作区必然带文件系统。
func TestOpenWorkspaceRequiresFS(t *testing.T) {
	_, err := ndrive.OpenWorkspace(context.Background(), "demo", "u1", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: ":memory:"},
	})
	if err == nil {
		t.Fatal("want error when FS config is missing")
	}
}

// TestWorkspaceFSCreatesRootAndConventionDirs 验证工作区文件系统会把根目录
// 和 /.tmp、/.mem 两个约定目录准备好。
func TestWorkspaceFSCreatesRootAndConventionDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	ws := openWS(t, root)

	fsvc := ws.FS()
	if fsvc == nil {
		t.Fatal("Workspace.FS() should not be nil")
	}
	for _, dir := range []string{fs.TmpDir, fs.MemDir} {
		entry, err := fsvc.Stat(context.Background(), dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !entry.IsDir {
			t.Errorf("%s should be a directory", dir)
		}
	}
}

// TestWorkspaceFSWriteRead 验证工作区文件系统可用：写入后能读回。
func TestWorkspaceFSWriteRead(t *testing.T) {
	ctx := context.Background()
	ws := openWS(t, filepath.Join(t.TempDir(), "ws"))
	fsvc := ws.FS()

	if _, err := fsvc.Write(ctx, "notes/a.txt", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := fsvc.Read(ctx, "notes/a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("Read = %q, want %q", data, "hello")
	}
}

// TestWorkspaceCloseIsIdempotent 验证 Close 可重复调用（文件系统 watcher
// 的释放是幂等的）。
func TestWorkspaceCloseIsIdempotent(t *testing.T) {
	ws, err := ndrive.OpenWorkspace(context.Background(), "demo", "u1", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: ":memory:"},
		FS:   local.Config{Path: filepath.Join(t.TempDir(), "ws")},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
