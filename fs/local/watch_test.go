package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wyx0k/ndrive/fs"
)

// newWatchFS 起一个开了监听的本地文件系统。
func newWatchFS(t *testing.T) *FS {
	t.Helper()
	f, err := New(filepath.Join(t.TempDir(), "ws"), WithWatch(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// waitFor 等待 path 的事件，超时返回 false。
func waitFor(t *testing.T, f *FS, path string, timeout time.Duration) (fs.Event, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-f.Events():
			if !ok {
				t.Fatal("events channel closed")
			}
			if ev.Path == path {
				return ev, true
			}
		case <-deadline:
			return fs.Event{}, false
		}
	}
}

// TestWatchReportsFileInNewDirectory 验证"新建目录 + 立刻往里写文件"能被
// 监听到。
//
// 底层 watcher 是收到目录创建事件之后才把目录挂上监听的，所以"目录建好
// 之后、监听挂上之前"写进去的文件它看不到 —— fs.Write 里的建目录和写文件
// 是挨着执行的，正好落在这个窗口里。local 用补扫兜住这个窗口。
func TestWatchReportsFileInNewDirectory(t *testing.T) {
	f := newWatchFS(t)
	ctx := context.Background()

	if _, err := f.Write(ctx, "notes/a.txt", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	ev, ok := waitFor(t, f, "notes/a.txt", 5*time.Second)
	if !ok {
		t.Fatal("timeout waiting for notes/a.txt event")
	}
	if ev.Op != fs.OpCreated {
		t.Errorf("op = %v, want created", ev.Op)
	}
}

// TestWatchReportsFileInExistingDirectory 验证已有目录里的新文件也能被监听到，
// 且报的是"创建"而不是"修改"。
func TestWatchReportsFileInExistingDirectory(t *testing.T) {
	f := newWatchFS(t)
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Join(f.Root(), "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // 让 watcher 把 notes 挂上

	if _, err := f.Write(ctx, "notes/b.txt", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	ev, ok := waitFor(t, f, "notes/b.txt", 5*time.Second)
	if !ok {
		t.Fatal("timeout waiting for notes/b.txt event")
	}
	if ev.Op != fs.OpCreated {
		t.Errorf("op = %v, want created", ev.Op)
	}
}

// TestWatchReportsEveryChangedFile 验证同一个窗口里改多个文件，每个文件都会
// 有事件 —— 底层 watcher 自带的全局防抖会把同一批里其它文件的事件全丢掉。
func TestWatchReportsEveryChangedFile(t *testing.T) {
	f := newWatchFS(t)
	ctx := context.Background()

	for _, name := range []string{"x.txt", "y.txt", "z.txt"} {
		if _, err := f.Write(ctx, name, []byte("hello")); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}

	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < 3 {
		select {
		case ev, ok := <-f.Events():
			if !ok {
				t.Fatal("events channel closed")
			}
			seen[ev.Path] = true
		case <-deadline:
			t.Fatalf("timeout, only got %v", seen)
		}
	}
	for _, name := range []string{"x.txt", "y.txt", "z.txt"} {
		if !seen[name] {
			t.Errorf("no event for %s (got %v)", name, seen)
		}
	}
}

// TestWatchReportsRemoval 验证删除也有事件。
func TestWatchReportsRemoval(t *testing.T) {
	f := newWatchFS(t)
	ctx := context.Background()

	if _, err := f.Write(ctx, "gone.txt", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := waitFor(t, f, "gone.txt", 5*time.Second); !ok {
		t.Fatal("timeout waiting for gone.txt event")
	}

	if err := f.Delete(ctx, "gone.txt", false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-f.Events():
			if !ok {
				t.Fatal("events channel closed")
			}
			if ev.Path == "gone.txt" && ev.Op == fs.OpRemoved {
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for removal event")
		}
	}
}

// TestWatchReportsConventionDirs 验证约定目录（点目录）里的变更也会发出去。
func TestWatchReportsConventionDirs(t *testing.T) {
	f := newWatchFS(t)
	ctx := context.Background()

	if _, err := f.Write(ctx, fs.MemDir+"/note.md", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := waitFor(t, f, fs.MemDir+"/note.md", 5*time.Second); !ok {
		t.Fatalf("timeout waiting for %s/note.md event", fs.MemDir)
	}
}
