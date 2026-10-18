package e2e_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/wyx0k/ndrive/meta"
	sqlitestore "github.com/wyx0k/ndrive/meta/sqlite"
)

// TestWorkspaceMetaPersistence 验证 ws 元信息持久化内容包含 userID。
// 走 meta.WorkspaceStore.Register facade。
func TestWorkspaceMetaPersistence(t *testing.T) {
	s, err := sqlitestore.New(context.Background(), sqlitestore.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	defer s.Close()

	ws := meta.NewWorkspaceStore(s)
	ctx := context.Background()
	if err := ws.Register(ctx, "fresh", "alice"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 直接读 key 验证内容（value 是 userID 字符串，不是 JSON）
	rec, err := s.Get(ctx, meta.WorkspaceKey("fresh"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Value == nil {
		t.Fatal("workspace meta not persisted")
	}
	if !bytes.Contains(rec.Value, []byte("alice")) {
		t.Errorf("persisted meta should contain userID 'alice', got %s", rec.Value)
	}
}
