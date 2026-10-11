package ntd_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ntd"
)

func collectEvents(ch <-chan ntd.UpdateEvent, dur time.Duration) []ntd.UpdateEvent {
	var events []ntd.UpdateEvent
	deadline := time.After(dur)
	for {
		select {
		case e := <-ch:
			events = append(events, e)
		case <-deadline:
			return events
		}
	}
}

func hasEvent(events []ntd.UpdateEvent, path string, op ntd.UpdateOp) bool {
	for _, e := range events {
		if e.Path == path && e.Op == op {
			return true
		}
	}
	return false
}

func TestLocalFileWatchExternal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	ws, err := ntd.OpenWorkspace(ctx, t.Name(), "owner-test", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer ws.Close()

	ws.SetPolicy(ctx, ntd.W)
	_, err = ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "watch",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: dir},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}

	events := make(chan ntd.UpdateEvent, 32)
	_, unsub := ws.Bus().Subscribe("tree.update", func(payload any) {
		if e, ok := payload.(ntd.UpdateEvent); ok {
			select {
			case events <- e:
			default:
			}
		}
	})
	defer unsub()

	// 外部直接创建文件
	testFile := filepath.Join(dir, "external.md")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := collectEvents(events, 500*time.Millisecond)
	t.Logf("create events: %v", got)
	if !hasEvent(got, "/external.md", ntd.UpdateCreate) && !hasEvent(got, "/external.md", ntd.UpdateWrite) {
		t.Errorf("expected create/write event for /external.md, got %v", got)
	}

	// 外部直接删除文件
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got = collectEvents(events, 500*time.Millisecond)
	t.Logf("delete events: %v", got)
	if !hasEvent(got, "/external.md", ntd.UpdateDelete) && !hasEvent(got, "/external.md", ntd.UpdateRename) {
		t.Errorf("expected delete/rename event for /external.md, got %v", got)
	}

	// 外部创建子目录后写入文件，验证递归监听
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // 等待 watcher 注册新目录
	subFile := filepath.Join(subDir, "note.md")
	if err := os.WriteFile(subFile, []byte("sub"), 0644); err != nil {
		t.Fatalf("WriteFile sub: %v", err)
	}
	got = collectEvents(events, 500*time.Millisecond)
	t.Logf("subdir events: %v", got)
	if !hasEvent(got, "/sub/note.md", ntd.UpdateCreate) && !hasEvent(got, "/sub/note.md", ntd.UpdateWrite) {
		t.Errorf("expected event for /sub/note.md, got %v", got)
	}
}

func TestLocalFileWatchViaCall(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	ws, err := ntd.OpenWorkspace(ctx, t.Name(), "owner-test", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer ws.Close()

	ws.SetPolicy(ctx, ntd.W)
	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "watch",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: dir},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}

	events := make(chan ntd.UpdateEvent, 32)
	_, unsub := ws.Bus().Subscribe("tree.update", func(payload any) {
		if e, ok := payload.(ntd.UpdateEvent); ok {
			select {
			case events <- e:
			default:
			}
		}
	})
	defer unsub()

	ownerCtx := ws.Context(ctx)

	// dir-mkdir → UpdateCreate
	if _, err := bm.Call(ownerCtx, "dir-mkdir", "/docs", nil); err != nil {
		t.Fatalf("dir-mkdir: %v", err)
	}
	got := collectEvents(events, 300*time.Millisecond)
	t.Logf("mkdir events: %v", got)
	if !hasEvent(got, "/docs", ntd.UpdateCreate) {
		t.Errorf("expected UpdateCreate for /docs, got %v", got)
	}

	// file-fm-write → UpdateWrite
	if _, err := bm.Call(ownerCtx, "file-fm-write", "/docs/hello.md", map[string]any{
		"body": "hello",
	}); err != nil {
		t.Fatalf("file-fm-write: %v", err)
	}
	got = collectEvents(events, 300*time.Millisecond)
	t.Logf("write events: %v", got)
	if !hasEvent(got, "/docs/hello.md", ntd.UpdateWrite) {
		t.Errorf("expected UpdateWrite for /docs/hello.md, got %v", got)
	}

	// dir-rm → UpdateDelete
	if _, err := bm.Call(ownerCtx, "dir-rm", "/docs", nil); err != nil {
		t.Fatalf("dir-rm: %v", err)
	}
	got = collectEvents(events, 300*time.Millisecond)
	t.Logf("rm events: %v", got)
	if !hasEvent(got, "/docs", ntd.UpdateDelete) {
		t.Errorf("expected UpdateDelete for /docs, got %v", got)
	}
}


