package ntd_test

import (
	"context"
	"testing"

	"ntd"
)

func openFreshWorkspace(t *testing.T, id string) *ntd.Workspace {
	t.Helper()
	ws, err := ntd.OpenWorkspace(context.Background(), id, "owner-test", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func TestWorkspaceOpenAndClose(t *testing.T) {
	ws := openFreshWorkspace(t, t.Name())
	if ws.ID() != t.Name() {
		t.Fatalf("want id=%s got %s", t.Name(), ws.ID())
	}
	if ws.OwnerID() != "owner-test" {
		t.Fatalf("want ownerID=owner-test got %s", ws.OwnerID())
	}
}

func TestWorkspaceOwnerMismatch(t *testing.T) {
	ctx := context.Background()
	ws, err := ntd.OpenWorkspace(ctx, t.Name(), "owner-a", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	ws.Close()

	// 同一 wsID 不同 ownerID，同一 store 路径会拒绝
	// 用不同 store 路径模拟已持久化的 workspace
	// （内存 DB 每次独立，此测试验证 ownerID 不匹配逻辑需持久化 store）
	t.Skip("需要持久化 store 才能跨进程验证 ownerID 不匹配，此场景依赖集成测试")
}

func TestCreateAndGetBizmodel(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	ws.SetPolicy(ctx, ntd.W)

	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "notes",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}
	if bm.ID() != "notes" {
		t.Fatalf("want id=notes got %s", bm.ID())
	}

	// GetBizmodel 应返回同一个
	bm2, err := ws.GetBizmodel(ctx, "notes")
	if err != nil {
		t.Fatalf("GetBizmodel: %v", err)
	}
	if bm2.ID() != "notes" {
		t.Fatalf("want id=notes got %s", bm2.ID())
	}
}

func TestCreateBizmodelDuplicate(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())

	opts := ntd.BizmodelOpts{
		ID: "dup", TypeName: ntd.BackendLocalFile,
		Config: ntd.LocalFileConfig{RootDir: t.TempDir()},
	}
	if _, err := ws.CreateBizmodel(ctx, opts); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := ws.CreateBizmodel(ctx, opts)
	if err == nil {
		t.Fatal("expected ErrBizmodelExists")
	}
}

func TestListAndDeleteBizmodel(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())

	for _, id := range []string{"a", "b", "c"} {
		_, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
			ID: id, TypeName: ntd.BackendLocalFile,
			Config: ntd.LocalFileConfig{RootDir: t.TempDir()},
		})
		if err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	list, err := ws.ListBizModels(ctx)
	if err != nil {
		t.Fatalf("ListBizModels: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 bizmodels got %d", len(list))
	}

	if err := ws.DeleteBizmodel(ctx, "b"); err != nil {
		t.Fatalf("DeleteBizmodel: %v", err)
	}
	list, _ = ws.ListBizModels(ctx)
	if len(list) != 2 {
		t.Fatalf("want 2 after delete got %d", len(list))
	}
}

func TestPolicySetAndGet(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())

	for _, mode := range []ntd.PolicyMode{ntd.RAsk, ntd.R, ntd.WAsk, ntd.W} {
		if err := ws.SetPolicy(ctx, mode); err != nil {
			t.Fatalf("SetPolicy(%v): %v", mode, err)
		}
		got, err := ws.GetPolicy(ctx)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if got != mode {
			t.Fatalf("want %v got %v", mode, got)
		}
	}
}

func TestPolicyEnforcement(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	dir := t.TempDir()

	bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID: "notes", TypeName: ntd.BackendLocalFile,
		Config: ntd.LocalFileConfig{RootDir: dir},
	})
	ownerCtx := ws.Context(ctx)

	// R 模式：写操作被拒绝
	ws.SetPolicy(ctx, ntd.R)
	if _, err := bm.Call(ownerCtx, "dir-mkdir", "/test", nil); err == nil {
		t.Fatal("expected denial under R policy")
	}

	// W 模式：写操作放行
	ws.SetPolicy(ctx, ntd.W)
	if _, err := bm.Call(ownerCtx, "dir-mkdir", "/test", nil); err != nil {
		t.Fatalf("expected success under W policy: %v", err)
	}
}

func TestBizmodelCallAndTree(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	ws.SetPolicy(ctx, ntd.W)

	bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID: "notes", TypeName: ntd.BackendLocalFile,
		Config: ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	ownerCtx := ws.Context(ctx)

	if _, err := bm.Call(ownerCtx, "dir-mkdir", "/notes", nil); err != nil {
		t.Fatalf("dir-mkdir: %v", err)
	}
	if _, err := bm.Call(ownerCtx, "file-fm-write", "/notes/hello.md", map[string]any{
		"withfm": true,
		"fm":     map[string]any{"title": "Hello"},
		"body":   "# Hello\n",
	}); err != nil {
		t.Fatalf("file-fm-write: %v", err)
	}

	briefs, err := bm.Tree().ListWithBrief(ownerCtx, "/notes")
	if err != nil {
		t.Fatalf("ListWithBrief: %v", err)
	}
	if len(briefs) == 0 {
		t.Fatal("expected at least one brief")
	}

	results, err := bm.Tree().Search(ownerCtx, "*hello*")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for *hello*")
	}
}
