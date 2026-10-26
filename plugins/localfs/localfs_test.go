package localfs_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/plugins/localfs"
	"github.com/wyx0k/ndrive/tools"
	"github.com/wyx0k/ndrive/tools/builtin"
)

// busContext 是只提供 Bus() 的最小 tools.Context 实现，用于测试 StatefulPlugin。
type busContext struct {
	b bus.Bus
}

func (c *busContext) Bus() bus.Bus                { return c.b }
func (c *busContext) Checker() permission.Checker { return nil }
func (c *busContext) API() *tools.ToolsAPI         { return nil }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// --- 路径沙盒 ---

func TestResolveRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fs.Read(context.Background(), "../outside.txt"); !errors.Is(err, localfs.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := fs.Stat(context.Background(), "a/../../b"); !errors.Is(err, localfs.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

// --- List 分页 ---

func TestListPagination(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 45; i++ {
		writeFile(t, filepath.Join(dir, "f"+pad(i)+".txt"), "x")
	}
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	page1, err := fs.List(context.Background(), "", 1, 20)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Entries) != 20 {
		t.Errorf("page1 len = %d, want 20", len(page1.Entries))
	}
	if page1.Total != 45 || page1.TotalPages != 3 {
		t.Errorf("page1 Total/TotalPages = %d/%d, want 45/3", page1.Total, page1.TotalPages)
	}

	page3, err := fs.List(context.Background(), "", 3, 20)
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3.Entries) != 5 {
		t.Errorf("page3 len = %d, want 5", len(page3.Entries))
	}

	// 默认页大小：pageSize <= 0 时使用 20
	def, err := fs.List(context.Background(), "", 1, 0)
	if err != nil {
		t.Fatalf("List default pageSize: %v", err)
	}
	if def.PageSize != 20 || len(def.Entries) != 20 {
		t.Errorf("default PageSize/len = %d/%d, want 20/20", def.PageSize, len(def.Entries))
	}
}

func pad(i int) string {
	s := "00" + itoa(i)
	return s[len(s)-2:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestListDirFlagAndBrief(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "note.md"), strings.Repeat("A", 150))
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page, err := fs.List(context.Background(), "", 1, 20)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var gotFile, gotDir bool
	for _, e := range page.Entries {
		switch e.Name {
		case "note.md":
			gotFile = true
			if e.IsDir {
				t.Error("note.md should not be dir")
			}
			if len(e.Brief) != 100 {
				t.Errorf("Brief len = %d, want 100", len(e.Brief))
			}
		case "sub":
			gotDir = true
			if !e.IsDir {
				t.Error("sub should be dir")
			}
		}
	}
	if !gotFile || !gotDir {
		t.Fatalf("missing entries: file=%v dir=%v", gotFile, gotDir)
	}
}

// --- Read 大小限制 ---

func TestReadTooLarge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "big.bin"), strings.Repeat("x", 100))

	fs, err := localfs.New(dir, localfs.WithMaxReadSize(10))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fs.Read(context.Background(), "big.bin"); !errors.Is(err, localfs.ErrFileTooLarge) {
		t.Errorf("want ErrFileTooLarge, got %v", err)
	}
}

func TestReadOK(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello world")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data, err := fs.Read(context.Background(), "a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Read = %q", data)
	}
}

// --- Open 流式读取 ---

func TestOpenStream(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "stream.txt"), "streamed content")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rc, err := fs.Open(context.Background(), "stream.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()

	buf := make([]byte, 64)
	n, _ := rc.Read(buf)
	if string(buf[:n]) != "streamed content" {
		t.Errorf("Open read = %q", buf[:n])
	}
}

// --- Search ---

func TestSearchByName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "report.md"), "irrelevant body")
	writeFile(t, filepath.Join(dir, "other.txt"), "irrelevant body")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results, err := fs.Search(context.Background(), "report", false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Name != "report.md" {
		t.Fatalf("Search name-only = %+v", results)
	}
}

func TestSearchByContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "nothing special here")
	writeFile(t, filepath.Join(dir, "b.txt"), "contains needle phrase")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 默认不做内容搜索，查不到
	noHit, err := fs.Search(context.Background(), "needle", false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(noHit) != 0 {
		t.Fatalf("name-only search should not match content, got %+v", noHit)
	}

	hit, err := fs.Search(context.Background(), "needle", true)
	if err != nil {
		t.Fatalf("Search contentSearch: %v", err)
	}
	if len(hit) != 1 || hit[0].Name != "b.txt" {
		t.Fatalf("Search contentSearch = %+v", hit)
	}
}

// --- Write / WriteStream / Edit ---

func TestWriteCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	entry, err := fs.Write(context.Background(), "sub/a.txt", []byte("v1"))
	if err != nil {
		t.Fatalf("Write create: %v", err)
	}
	if entry.Path != "sub/a.txt" || entry.Size != 2 {
		t.Fatalf("Write create entry = %+v", entry)
	}
	data, err := fs.Read(context.Background(), "sub/a.txt")
	if err != nil || string(data) != "v1" {
		t.Fatalf("Read after create = %q, %v", data, err)
	}

	if _, err := fs.Write(context.Background(), "sub/a.txt", []byte("v2")); err != nil {
		t.Fatalf("Write overwrite: %v", err)
	}
	data, err = fs.Read(context.Background(), "sub/a.txt")
	if err != nil || string(data) != "v2" {
		t.Fatalf("Read after overwrite = %q, %v", data, err)
	}
}

func TestWriteRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fs.Write(context.Background(), "../outside.txt", []byte("x")); !errors.Is(err, localfs.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

func TestWriteStream(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	entry, err := fs.WriteStream(context.Background(), "stream.txt", strings.NewReader("streamed body"))
	if err != nil {
		t.Fatalf("WriteStream: %v", err)
	}
	if entry.Size != int64(len("streamed body")) {
		t.Fatalf("WriteStream entry.Size = %d, want %d", entry.Size, len("streamed body"))
	}
	data, err := fs.Read(context.Background(), "stream.txt")
	if err != nil || string(data) != "streamed body" {
		t.Fatalf("Read after WriteStream = %q, %v", data, err)
	}
}

func TestEditReplacesUniqueOccurrence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello world")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before, err := fs.Stat(context.Background(), "doc.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	after, err := fs.Edit(context.Background(), "doc.md", "world", "ndrive", before.ModTime)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	data, err := fs.Read(context.Background(), "doc.md")
	if err != nil || string(data) != "hello ndrive" {
		t.Fatalf("Read after Edit = %q, %v", data, err)
	}
	if after.ModTime.Equal(before.ModTime) {
		t.Error("Edit should update ModTime")
	}
}

func TestEditRejectsStaleModTime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello world")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stale := time.Now().Add(-time.Hour)

	if _, err := fs.Edit(context.Background(), "doc.md", "world", "ndrive", stale); !errors.Is(err, localfs.ErrModTimeMismatch) {
		t.Errorf("want ErrModTimeMismatch, got %v", err)
	}
	data, err := fs.Read(context.Background(), "doc.md")
	if err != nil || string(data) != "hello world" {
		t.Fatalf("file should be unchanged after rejected Edit, got %q, %v", data, err)
	}
}

func TestEditRejectsMissingOld(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello world")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before, err := fs.Stat(context.Background(), "doc.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if _, err := fs.Edit(context.Background(), "doc.md", "nope", "x", before.ModTime); !errors.Is(err, localfs.ErrOldNotFound) {
		t.Errorf("want ErrOldNotFound, got %v", err)
	}
}

func TestEditRejectsNonUniqueOld(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello hello")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before, err := fs.Stat(context.Background(), "doc.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if _, err := fs.Edit(context.Background(), "doc.md", "hello", "x", before.ModTime); !errors.Is(err, localfs.ErrOldNotUnique) {
		t.Errorf("want ErrOldNotUnique, got %v", err)
	}
}

// --- Delete ---

func TestDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "a.txt", false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone after Delete, stat err = %v", err)
	}
}

func TestDeleteRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "outside.txt")
	writeFile(t, outside, "keep me")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "../outside.txt", false); !errors.Is(err, localfs.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should still exist: %v", err)
	}
}

func TestDeleteRejectsRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, path := range []string{"", ".", "./"} {
		if err := fs.Delete(context.Background(), path, true); !errors.Is(err, localfs.ErrDeleteRoot) {
			t.Errorf("Delete(%q) want ErrDeleteRoot, got %v", path, err)
		}
	}
	// root 里的内容必须还在
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("root content should still exist: %v", err)
	}
}

// TestDeleteDirRequiresRecursive 验证删目录必须显式 recursive=true：
// 不加 recursive 时目录（及其内容）保持原样。
func TestDeleteDirRequiresRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "a.txt"), "hello")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "sub", false); !errors.Is(err, localfs.ErrIsDirectory) {
		t.Errorf("want ErrIsDirectory, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "a.txt")); err != nil {
		t.Fatalf("dir content should be untouched: %v", err)
	}
}

func TestDeleteDirRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "deep", "a.txt"), "hello")
	writeFile(t, filepath.Join(dir, "keep.txt"), "keep")

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "sub", true); err != nil {
		t.Fatalf("Delete recursive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("sibling should survive: %v", err)
	}
}

// TestDeleteMissingPath 验证删不存在的路径报错（而不是静默成功）。
func TestDeleteMissingPath(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "nope.txt", false); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist, got %v", err)
	}
}

// TestDeleteSymlinkKeepsTarget 验证指向目录的符号链接只删链接本身，
// 不动链接目标（避免软链把删除操作放大到沙盒外）。
func TestDeleteSymlinkKeepsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation on Windows needs extra privileges")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeFile(t, filepath.Join(target, "a.txt"), "hello")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "link", true); err != nil {
		t.Fatalf("Delete symlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "a.txt")); err != nil {
		t.Fatalf("symlink target should survive: %v", err)
	}
}

// --- Service / tools.Plugin wiring ---

func TestNewPluginRegistersTenTools(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	if len(plugin.Tools()) != 10 {
		t.Fatalf("len(plugin.Tools()) = %d, want 10", len(plugin.Tools()))
	}

	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	svc, err := api.Service("store.localfs")
	if err != nil {
		t.Fatalf("Service: %v", err)
	}
	if svc == nil {
		t.Fatal("Service() returned nil")
	}
}

func TestListToolCall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.txt"), "hi")

	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	got, err := api.Call(context.Background(), "store.localfs.list", map[string]any{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got == nil {
		t.Fatal("Call returned nil")
	}
}

func TestOpenToolIsInternal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.txt"), "hidden stream")

	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	for _, info := range api.Tools() {
		if info.ID == "store.localfs.open-stream" {
			t.Fatal("store.localfs.open-stream should not appear in Tools() (Internal)")
		}
	}
	for _, fn := range api.Funcs() {
		if fn.ID == "store.localfs.open-stream" {
			t.Fatal("store.localfs.open-stream should not appear in Funcs() (Internal)")
		}
	}

	got, err := api.Call(context.Background(), "store.localfs.open-stream", map[string]any{"path": "x.txt"})
	if err != nil {
		t.Fatalf("Call store.localfs.open-stream: %v", err)
	}
	rc, ok := got.(io.ReadCloser)
	if !ok {
		t.Fatalf("Call result type = %T, want io.ReadCloser", got)
	}
	defer rc.Close()

	buf := make([]byte, 64)
	n, _ := rc.Read(buf)
	if string(buf[:n]) != "hidden stream" {
		t.Errorf("open read = %q", buf[:n])
	}
}

func TestWriteToolCall(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	if _, err := api.Call(context.Background(), "store.localfs.write", map[string]any{
		"path": "a.txt", "body": "hello",
	}); err != nil {
		t.Fatalf("Call write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("written file = %q, %v", data, err)
	}
}

func TestWriteStreamToolIsInternalAndCallable(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	for _, info := range api.Tools() {
		if info.ID == "store.localfs.write-stream" {
			t.Fatal("store.localfs.write-stream should not appear in Tools() (Internal)")
		}
	}

	_, err = api.Call(context.Background(), "store.localfs.write-stream", map[string]any{
		"path": "stream.txt", "reader": strings.NewReader("streamed via tool"),
	})
	if err != nil {
		t.Fatalf("Call write-stream: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "stream.txt"))
	if err != nil || string(data) != "streamed via tool" {
		t.Fatalf("written file = %q, %v", data, err)
	}
}

func TestWriteMarkdownToolWithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	_, err = api.Call(context.Background(), "store.localfs.write-markdown", map[string]any{
		"path":   "note.md",
		"withfm": true,
		"fm":     map[string]any{"title": "My Note"},
		"body":   "# Hello\n",
	})
	if err != nil {
		t.Fatalf("Call write-markdown: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "note.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "---\n") || !strings.Contains(got, "title: My Note") || !strings.Contains(got, "# Hello\n") {
		t.Fatalf("write-markdown with frontmatter output = %q", got)
	}
}

func TestWriteMarkdownToolWithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	_, err = api.Call(context.Background(), "store.localfs.write-markdown", map[string]any{
		"path": "plain.md", "body": "# Hello\n",
	})
	if err != nil {
		t.Fatalf("Call write-markdown: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "plain.md"))
	if err != nil || string(data) != "# Hello\n" {
		t.Fatalf("write-markdown without frontmatter output = %q, %v", data, err)
	}
}

func TestEditToolCallRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello world")

	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	statRes, err := api.Call(context.Background(), "store.localfs.stat", map[string]any{"path": "doc.md"})
	if err != nil {
		t.Fatalf("Call stat: %v", err)
	}
	entry, ok := statRes.(builtin.FileEntry)
	if !ok {
		t.Fatalf("stat result type = %T", statRes)
	}

	_, err = api.Call(context.Background(), "store.localfs.edit", map[string]any{
		"path": "doc.md", "old": "world", "new": "ndrive",
		"modTime": entry.ModTime.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("Call edit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "doc.md"))
	if err != nil || string(data) != "hello ndrive" {
		t.Fatalf("file after edit = %q, %v", data, err)
	}
}

func TestEditToolCallRejectsStaleModTime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello world")

	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	stale := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	_, err = api.Call(context.Background(), "store.localfs.edit", map[string]any{
		"path": "doc.md", "old": "world", "new": "ndrive", "modTime": stale,
	})
	if !errors.Is(err, localfs.ErrModTimeMismatch) {
		t.Errorf("want ErrModTimeMismatch, got %v", err)
	}
}

// TestDeleteToolCall 验证 store.localfs.delete 对 LLM 可见并能删文件。
func TestDeleteToolCall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")

	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	// delete 是导出 tool（LLM 可见）
	visible := false
	for _, info := range api.Tools() {
		if info.ID == "store.localfs.delete" {
			visible = true
		}
	}
	if !visible {
		t.Fatal("store.localfs.delete should appear in Tools()")
	}

	if _, err := api.Call(context.Background(), "store.localfs.delete", map[string]any{
		"path": "a.txt",
	}); err != nil {
		t.Fatalf("Call delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should be gone after delete tool, stat err = %v", err)
	}
}

// TestDeleteToolRejectsDirWithoutRecursive 验证 delete tool 删目录时
// 不传 recursive 会被拒绝（tool 层把 FS 的硬约束透出来）。
func TestDeleteToolRejectsDirWithoutRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "a.txt"), "hello")

	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	api := tools.NewToolsAPI(&busContext{b: bus.New()})
	if err := api.Register(plugin, nil); err != nil {
		t.Fatalf("Register %s: %v", plugin.ID(), err)
	}

	_, err = api.Call(context.Background(), "store.localfs.delete", map[string]any{"path": "sub"})
	if !errors.Is(err, localfs.ErrIsDirectory) {
		t.Errorf("want ErrIsDirectory, got %v", err)
	}

	if _, err := api.Call(context.Background(), "store.localfs.delete", map[string]any{
		"path": "sub", "recursive": true,
	}); err != nil {
		t.Fatalf("Call delete recursive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone, stat err = %v", err)
	}
}

// --- 文件系统监听 ---

func TestWatcherPublishesFileEvents(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

	b := bus.New()
	ws := &busContext{b: b}

	// 新文件在不同平台上报告的 op 不一致（Linux 通常先 Create 再 Write，
	// Windows 的 fsnotify 常把新文件写入合并成单个 Write 事件），所以这里
	// 同时订阅 created 和 modified，只要收到其中一个即视为测试通过。
	received := make(chan map[string]any, 2)
	onEvent := func(payload any) {
		if m, ok := payload.(map[string]any); ok {
			select {
			case received <- m:
			default:
			}
		}
	}
	b.Subscribe("file.created", onEvent)
	b.Subscribe("file.modified", onEvent)

	cfg := localfs.Config{Root: dir, DebounceInterval: 50 * time.Millisecond}
	sp, ok := plugin.(tools.StatefulPlugin)
	if !ok {
		t.Fatalf("plugin %s does not implement StatefulPlugin", plugin.ID())
	}
	if err := sp.StartPlugin(ws, cfg); err != nil {
		t.Fatalf("StartPlugin %s: %v", plugin.ID(), err)
	}
	defer func() { _ = sp.StopPlugin() }()

	writeFile(t, filepath.Join(dir, "new.txt"), "new content")

	select {
	case payload := <-received:
		path, _ := payload["path"].(string)
		if path != "new.txt" {
			t.Errorf("event path = %q, want new.txt", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for file change event")
	}
}

// TestStopPluginReleasesWatcher 验证 StopPlugin 真的关闭了文件监听资源：
// 停止之后再写文件，不应再收到 file.* 事件（watcher 之前一直运行会持续
// 收到事件，之前的测试只 defer 调用 StopPlugin 从没断言过停止效果）。
func TestStopPluginReleasesWatcher(t *testing.T) {
	dir := t.TempDir()
	plugin, err := localfs.NewPlugin(dir)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

	b := bus.New()
	ws := &busContext{b: b}

	received := make(chan map[string]any, 4)
	onEvent := func(payload any) {
		if m, ok := payload.(map[string]any); ok {
			select {
			case received <- m:
			default:
			}
		}
	}
	b.Subscribe("file.created", onEvent)
	b.Subscribe("file.modified", onEvent)

	cfg := localfs.Config{Root: dir, DebounceInterval: 50 * time.Millisecond}
	sp, ok := plugin.(tools.StatefulPlugin)
	if !ok {
		t.Fatalf("plugin %s does not implement StatefulPlugin", plugin.ID())
	}
	if err := sp.StartPlugin(ws, cfg); err != nil {
		t.Fatalf("StartPlugin: %v", err)
	}

	// 先确认 watcher 确实在跑：写一个文件应该能收到事件。
	writeFile(t, filepath.Join(dir, "before-stop.txt"), "a")
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initial file event; watcher not running")
	}

	if err := sp.StopPlugin(); err != nil {
		t.Fatalf("StopPlugin: %v", err)
	}

	// 排空 StopPlugin 之前可能已经在飞行中的事件，再等一个安静窗口。
	drain := time.After(200 * time.Millisecond)
	for drained := false; !drained; {
		select {
		case <-received:
		case <-drain:
			drained = true
		}
	}

	writeFile(t, filepath.Join(dir, "after-stop.txt"), "b")
	select {
	case payload := <-received:
		t.Fatalf("received event after StopPlugin, watcher not released: %+v", payload)
	case <-time.After(1 * time.Second):
		// 预期：停止后不再收到任何事件。
	}
}
