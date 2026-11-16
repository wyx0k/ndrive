package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wyx0k/ndrive/fs"
	"github.com/wyx0k/ndrive/fs/local"
)

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
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fs.Read(context.Background(), "../outside.txt"); !errors.Is(err, local.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := fs.Stat(context.Background(), "a/../../b"); !errors.Is(err, local.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

// --- List 分页 ---

func TestListPagination(t *testing.T) {
	dir := t.TempDir()
	// 列在子目录里 —— root 下还有 .tmp / .mem 两个约定目录，列 root 的话
	// 总数会被它们污染。
	sub := "files"
	for i := 0; i < 45; i++ {
		writeFile(t, filepath.Join(dir, sub, "f"+pad(i)+".txt"), "x")
	}
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	page1, err := fs.List(context.Background(), sub, 1, 20)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Entries) != 20 {
		t.Errorf("page1 len = %d, want 20", len(page1.Entries))
	}
	if page1.Total != 45 || page1.TotalPages != 3 {
		t.Errorf("page1 Total/TotalPages = %d/%d, want 45/3", page1.Total, page1.TotalPages)
	}

	page3, err := fs.List(context.Background(), sub, 3, 20)
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3.Entries) != 5 {
		t.Errorf("page3 len = %d, want 5", len(page3.Entries))
	}

	// 默认页大小：pageSize <= 0 时使用 20
	def, err := fs.List(context.Background(), sub, 1, 0)
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

	fs, err := local.New(dir)
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

	fs, err := local.New(dir, local.WithMaxReadSize(10))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fs.Read(context.Background(), "big.bin"); !errors.Is(err, local.ErrFileTooLarge) {
		t.Errorf("want ErrFileTooLarge, got %v", err)
	}
}

func TestReadOK(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello world")

	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
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
	fs, err := local.New(dir)
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
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fs.Write(context.Background(), "../outside.txt", []byte("x")); !errors.Is(err, local.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

func TestWriteStream(t *testing.T) {
	dir := t.TempDir()
	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stale := time.Now().Add(-time.Hour)

	if _, err := fs.Edit(context.Background(), "doc.md", "world", "ndrive", stale); !errors.Is(err, local.ErrModTimeMismatch) {
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

	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before, err := fs.Stat(context.Background(), "doc.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if _, err := fs.Edit(context.Background(), "doc.md", "nope", "x", before.ModTime); !errors.Is(err, local.ErrOldNotFound) {
		t.Errorf("want ErrOldNotFound, got %v", err)
	}
}

func TestEditRejectsNonUniqueOld(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.md"), "hello hello")

	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before, err := fs.Stat(context.Background(), "doc.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if _, err := fs.Edit(context.Background(), "doc.md", "hello", "x", before.ModTime); !errors.Is(err, local.ErrOldNotUnique) {
		t.Errorf("want ErrOldNotUnique, got %v", err)
	}
}

// --- Delete ---

func TestDeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")

	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "../outside.txt", false); !errors.Is(err, local.ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should still exist: %v", err)
	}
}

func TestDeleteRejectsRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")

	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, path := range []string{"", ".", "./"} {
		if err := fs.Delete(context.Background(), path, true); !errors.Is(err, local.ErrDeleteRoot) {
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

	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fs.Delete(context.Background(), "sub", false); !errors.Is(err, local.ErrIsDirectory) {
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

	fs, err := local.New(dir)
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
	fs, err := local.New(dir)
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

	fs, err := local.New(dir)
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

// --- 约定目录 ---

// TestNewCreatesRootAndConventionDirs 验证 fs-local 把 root 准备好：目录不存在
// 就创建，并保证 fs.TmpDir / fs.MemDir 两个约定目录存在（fs.FileService 契约）。
func TestNewCreatesRootAndConventionDirs(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "not", "exist", "yet")

	f, err := local.New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close()

	for _, dir := range []string{fs.TmpDir, fs.MemDir} {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("stat %s dir: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s should be a directory", dir)
		}
	}
}

// --- 文件系统监听 ---

// TestWatcherEmitsEvents 验证 WithWatch 打开后，文件变更能通过 Events()
// channel 冒出来，路径是相对 root 的沙盒路径。
func TestWatcherEmitsEvents(t *testing.T) {
	dir := t.TempDir()
	f, err := local.New(dir, local.WithWatch(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close()

	writeFile(t, filepath.Join(dir, "new.txt"), "new content")

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-f.Events():
			if !ok {
				t.Fatal("events channel closed unexpectedly")
			}
			// 新文件在不同平台上报告的 op 不一致（Linux 通常先 Created 再
			// Modified，Windows 的 fsnotify 常把新文件写入合并成单个
			// Modified），所以两种 op 都接受，只要路径对就算通过。
			if ev.Op != fs.OpCreated && ev.Op != fs.OpModified {
				continue
			}
			if ev.Path != "new.txt" {
				t.Errorf("event path = %q, want new.txt", ev.Path)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for file change event")
		}
	}
}

// TestCloseReleasesWatcher 验证 Close 真的释放了监听资源：关闭之后再写文件，
// 不应再收到事件。
func TestCloseReleasesWatcher(t *testing.T) {
	dir := t.TempDir()
	f, err := local.New(dir, local.WithWatch(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 先确认 watcher 确实在跑。
	writeFile(t, filepath.Join(dir, "before-close.txt"), "a")
	select {
	case <-f.Events():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initial file event; watcher not running")
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close 幂等。
	if err := f.Close(); err != nil {
		t.Fatalf("Close (second call): %v", err)
	}

	writeFile(t, filepath.Join(dir, "after-close.txt"), "b")
	select {
	case ev, ok := <-f.Events():
		if ok {
			t.Fatalf("received event after Close, watcher not released: %+v", ev)
		}
		// channel 已关闭 —— 这才是期望的终态。
	case <-time.After(time.Second):
		t.Fatal("events channel should be closed after Close")
	}
}

// TestEventsWithoutWatchIsClosed 验证未开启 WithWatch 时 Events() 返回一个
// 已关闭的 channel —— 接收方的 range 循环立即结束，不会挂住。
func TestEventsWithoutWatchIsClosed(t *testing.T) {
	dir := t.TempDir()
	f, err := local.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close()

	select {
	case _, ok := <-f.Events():
		if ok {
			t.Fatal("events channel should be closed when watch is disabled")
		}
	case <-time.After(time.Second):
		t.Fatal("events channel should be closed immediately")
	}
}
