package ndrive_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/fs"
	"github.com/wyx0k/ndrive/fs/local"
	"github.com/wyx0k/ndrive/meta/sqlite"
)

// --- 自研文件系统：证明 fs.Config / fs.FileService 是开放的契约 ---
//
// memFS 是一个最小的内存文件系统；memFSConfig 把它包装成 fs.Config。
// 外部包可以自己实现并接进工作区，不需要改根包。

type memFS struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	m := &memFS{files: map[string][]byte{}, dirs: map[string]bool{}}
	m.dirs[""] = true
	m.dirs[fs.TmpDir] = true
	m.dirs[fs.MemDir] = true
	return m
}

// memFSConfig 实现 fs.Config。
type memFSConfig struct {
	fs *memFS
}

func (c memFSConfig) NewFileService(context.Context) (fs.FileService, error) {
	return c.fs, nil
}

func (m *memFS) List(_ context.Context, path string, page, pageSize int) (fs.ListPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := path
	if prefix != "" {
		prefix += "/"
	}
	entries := []fs.FileEntry{}
	for p := range m.files {
		if !strings.HasPrefix(p, prefix) || strings.Contains(strings.TrimPrefix(p, prefix), "/") {
			continue
		}
		entries = append(entries, m.entry(p))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	total := len(entries)
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	start := min((page-1)*pageSize, total)
	end := min(start+pageSize, total)
	return fs.ListPage{
		Entries: entries[start:end], Page: page, PageSize: pageSize,
		Total: total, TotalPages: (total + pageSize - 1) / pageSize,
	}, nil
}

func (m *memFS) entry(path string) fs.FileEntry {
	return fs.FileEntry{
		Name: filepath.Base(path),
		Path: path,
		Size: int64(len(m.files[path])),
	}
}

func (m *memFS) Stat(_ context.Context, path string) (fs.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs[path] {
		return fs.FileEntry{Name: filepath.Base(path), Path: path, IsDir: true}, nil
	}
	if b, ok := m.files[path]; ok {
		return fs.FileEntry{Name: filepath.Base(path), Path: path, Size: int64(len(b))}, nil
	}
	return fs.FileEntry{}, errors.New("memfs: not found")
}

func (m *memFS) Read(_ context.Context, path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[path]
	if !ok {
		return nil, errors.New("memfs: not found")
	}
	return append([]byte(nil), b...), nil
}

func (m *memFS) Open(_ context.Context, path string) (io.ReadCloser, error) {
	b, err := m.Read(context.Background(), path)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (m *memFS) Search(_ context.Context, query string, _ bool) ([]fs.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := strings.ToLower(query)
	out := []fs.FileEntry{}
	for p := range m.files {
		if strings.Contains(strings.ToLower(p), q) {
			out = append(out, m.entry(p))
		}
	}
	return out, nil
}

func (m *memFS) Write(_ context.Context, path string, body []byte) (fs.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = append([]byte(nil), body...)
	return m.entry(path), nil
}

func (m *memFS) WriteStream(_ context.Context, path string, r io.Reader) (fs.FileEntry, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return fs.FileEntry{}, err
	}
	return m.Write(context.Background(), path, b)
}

func (m *memFS) Edit(_ context.Context, path, old, newText string, _ time.Time) (fs.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[path]
	if !ok {
		return fs.FileEntry{}, errors.New("memfs: not found")
	}
	s := string(b)
	if strings.Count(s, old) != 1 {
		return fs.FileEntry{}, errors.New("memfs: old text not unique")
	}
	m.files[path] = []byte(strings.Replace(s, old, newText, 1))
	return m.entry(path), nil
}

func (m *memFS) Delete(_ context.Context, path string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, path)
	return nil
}

// TestCustomFSConfigIsAccepted 验证外部实现的自研文件系统可以直接接进工作区
// —— fs.Config 是开放契约，根包不认识具体实现。
func TestCustomFSConfigIsAccepted(t *testing.T) {
	ctx := context.Background()
	ws, err := ndrive.OpenWorkspace(ctx, "demo", "u1", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: ":memory:"},
		FS:   memFSConfig{fs: newMemFS()},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace with custom fs: %v", err)
	}
	defer ws.Close()

	// 约定目录必须由实现自己准备好（根包会校验）。
	for _, dir := range []string{fs.TmpDir, fs.MemDir} {
		if _, err := ws.FS().Stat(ctx, dir); err != nil {
			t.Errorf("stat %s: %v", dir, err)
		}
	}
	if _, err := ws.FS().Write(ctx, "notes/a.txt", []byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := ws.FS().Read(ctx, "notes/a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("Read = %q, want hi", data)
	}
}

// --- fs 变更事件 → bus ---

// TestFSChangesPublishedOnBus 验证文件变更事件会经桥接发到 bus
// （topic fs.change），UI / agent 运行时可以直接订阅。
func TestFSChangesPublishedOnBus(t *testing.T) {
	ctx := context.Background()
	ws, err := ndrive.OpenWorkspace(ctx, "demo", "u1", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: ":memory:"},
		FS: local.Config{
			Path:          filepath.Join(t.TempDir(), "ws"),
			Watch:         true,
			WatchDebounce: 10 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer ws.Close()

	events := make(chan fs.Event, 16)
	_, unsub := ws.Bus().Subscribe(fs.TopicChange, func(p any) {
		if ev, ok := p.(fs.Event); ok {
			select {
			case events <- ev:
			default:
			}
		}
	})
	defer unsub()

	// 写一个文件（不经 tool，直接走工作区文件系统）。
	if _, err := ws.FS().Write(ctx, "a.txt", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// 再写一个到新建的子目录里 —— 这是最容易漏的一档：目录刚建好、
	// 监听还没挂上，文件就写进去了。
	if _, err := ws.FS().Write(ctx, "notes/b.md", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := []string{"a.txt", "notes/b.md"}
	seen := map[string]fs.Event{}
	deadline := time.After(10 * time.Second)
	for {
		got := 0
		for _, path := range want {
			if _, ok := seen[path]; ok {
				got++
			}
		}
		if got == len(want) {
			break
		}
		select {
		case ev := <-events:
			seen[ev.Path] = ev
		case <-deadline:
			t.Fatalf("timeout waiting for fs.change events, got %v", seen)
		}
	}
	for _, path := range want {
		ev, ok := seen[path]
		if !ok {
			t.Errorf("no event for %s", path)
			continue
		}
		if ev.Op != fs.OpCreated && ev.Op != fs.OpModified {
			t.Errorf("%s: op = %v, want created/modified", path, ev.Op)
		}
	}
}

// TestFSChangeEventJSON 验证事件可以 JSON 化（过 bus 到 UI 时人眼可读）。
func TestFSChangeEventJSON(t *testing.T) {
	ev := fs.Event{Op: fs.OpModified, Path: "notes/a.txt", Size: 5}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"op":"modified"`) {
		t.Errorf("json = %s, want op:modified", b)
	}
	if !strings.Contains(string(b), `"path":"notes/a.txt"`) {
		t.Errorf("json = %s, want path", b)
	}
}
