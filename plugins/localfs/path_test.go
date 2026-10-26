package localfs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// linkDir 在 dir 下建一个指向 target 的目录链接（name 为链接名）。
//
// Windows 上用 junction（mklink /J）：它不需要 SeCreateSymbolicLinkPrivilege，
// 而且 junction 正是 Windows 上最常见的"目录指向别处"手法——沙盒必须认它。
// 其它平台用软链。
func linkDir(t *testing.T, dir, name, target string) {
	t.Helper()
	link := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Skipf("cannot create junction %s -> %s: %v (%s)", link, target, err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink %s -> %s: %v", link, target, err)
	}
}

// --- withinRoot ---

func TestWithinRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	cases := []struct {
		abs  string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "a", "b.txt"), true},
		{filepath.Join(root, "..", "sibling"), false},
		{filepath.Join(filepath.Dir(root), "sibling"), false},
		{root + "-other", false}, // 同前缀但不是子目录
	}
	for _, c := range cases {
		if got := withinRoot(root, c.abs); got != c.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, c.abs, got, c.want)
		}
	}
}

// linkFile 在 dir 下建一个指向 target 的文件链接。
// Windows 上文件软链需要 SeCreateSymbolicLinkPrivilege，没有就跳过测试。
func linkFile(t *testing.T, dir, name, target string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Skipf("cannot create file symlink: %v", err)
	}
}

// --- evalLinks ---

func TestEvalLinksPlainPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := evalLinks(file)
	if err != nil {
		t.Fatalf("evalLinks: %v", err)
	}
	if got != file {
		t.Errorf("evalLinks = %q, want %q", got, file)
	}
}

// TestEvalLinksResolvesLink 验证链接被解开成真实目标。
func TestEvalLinksResolvesLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir(t, root, "link", target)

	got, err := evalLinks(filepath.Join(root, "link", "a.txt"))
	if err != nil {
		t.Fatalf("evalLinks: %v", err)
	}
	want := filepath.Join(target, "a.txt")
	if got != want {
		t.Errorf("evalLinks = %q, want %q", got, want)
	}
}

// TestEvalLinksDetectsCycle 验证链接成环时不会无限展开（深度上限兜底）。
func TestEvalLinksDetectsCycle(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")

	if runtime.GOOS == "windows" {
		// junction 的目标必须存在，先建 a→b，再把 b 换成 b→a 构成环。
		if err := os.MkdirAll(b, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", a, b).CombinedOutput(); err != nil {
			t.Skipf("cannot create junction: %v (%s)", err, out)
		}
		if err := os.Remove(b); err != nil {
			t.Fatalf("remove dir: %v", err)
		}
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", b, a).CombinedOutput(); err != nil {
			t.Skipf("cannot create junction: %v (%s)", err, out)
		}
	} else {
		if err := os.Symlink("b", a); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("a", b); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := evalLinks(a); err == nil {
		t.Error("evalLinks should fail on a link cycle")
	}
}

// --- realPath ---

func TestRealPathKeepsMissingTail(t *testing.T) {
	dir := t.TempDir()
	got, err := realPath(filepath.Join(dir, "not", "exist.txt"))
	if err != nil {
		t.Fatalf("realPath: %v", err)
	}
	want := filepath.Join(dir, "not", "exist.txt")
	if got != want {
		t.Errorf("realPath = %q, want %q", got, want)
	}
}

// --- 沙盒：链接不能把访问引到 root 之外 ---

// newEscaped 造一个 root + 一个指向 root 之外的目录链接，返回 (fs, 外部目录)。
func newEscaped(t *testing.T) (*FS, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir(t, root, "link", outside)
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fs, outside
}

func TestReadRejectsLinkEscape(t *testing.T) {
	fs, _ := newEscaped(t)
	_, err := fs.Read(context.Background(), "link/secret.txt")
	if !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

func TestStatRejectsLinkEscape(t *testing.T) {
	fs, _ := newEscaped(t)
	_, err := fs.Stat(context.Background(), "link/secret.txt")
	if !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

func TestListRejectsLinkEscape(t *testing.T) {
	fs, _ := newEscaped(t)
	_, err := fs.List(context.Background(), "link", 1, 20)
	if !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

func TestOpenRejectsLinkEscape(t *testing.T) {
	fs, _ := newEscaped(t)
	rc, err := fs.Open(context.Background(), "link/secret.txt")
	if err == nil {
		_ = rc.Close()
		t.Fatal("Open should fail")
	}
	if !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
}

func TestWriteRejectsLinkEscape(t *testing.T) {
	fs, outside := newEscaped(t)
	secret := filepath.Join(outside, "secret.txt")

	if _, err := fs.Write(context.Background(), "link/secret.txt", []byte("PWNED")); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	data, err := os.ReadFile(secret)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "TOP SECRET" {
		t.Errorf("outside file was modified: %q", data)
	}
}

func TestWriteStreamRejectsLinkEscape(t *testing.T) {
	fs, outside := newEscaped(t)
	secret := filepath.Join(outside, "secret.txt")

	if _, err := fs.WriteStream(context.Background(), "link/secret.txt", strings.NewReader("PWNED")); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	data, err := os.ReadFile(secret)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "TOP SECRET" {
		t.Errorf("outside file was modified: %q", data)
	}
}

func TestDeleteRejectsLinkEscape(t *testing.T) {
	fs, outside := newEscaped(t)
	secret := filepath.Join(outside, "secret.txt")

	if err := fs.Delete(context.Background(), "link/secret.txt", false); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("outside file should still exist: %v", err)
	}
}

// TestSearchDoesNotLeakOutsideContent 验证内容搜索不会顺着链接把 root 之外的
// 内容搜出来。
func TestSearchDoesNotLeakOutsideContent(t *testing.T) {
	fs, _ := newEscaped(t)
	results, err := fs.Search(context.Background(), "TOP SECRET", true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, e := range results {
		if strings.Contains(e.Path, "secret.txt") {
			t.Errorf("search leaked outside content: %+v", e)
		}
	}
}

// TestSearchDoesNotLeakOutsideBrief 验证 .md 文件的简介不会顺着链接把 root 之外
// 的内容带进搜索结果。
func TestSearchDoesNotLeakOutsideBrief(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "note.md"), []byte("LEAKED BRIEF"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkFile(t, root, "note.md", filepath.Join(outside, "note.md"))
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	results, err := fs.Search(context.Background(), "note", false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, e := range results {
		if e.Brief != "" {
			t.Errorf("brief leaked outside content: %+v", e)
		}
	}
}

// TestListDoesNotLeakOutsideBrief 同上，list 路径下也不能带出外部内容。
func TestListDoesNotLeakOutsideBrief(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "note.md"), []byte("LEAKED BRIEF"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkFile(t, root, "note.md", filepath.Join(outside, "note.md"))
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	page, err := fs.List(context.Background(), "", 1, 20)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range page.Entries {
		if e.Brief != "" {
			t.Errorf("brief leaked outside content: %+v", e)
		}
	}
}

// TestOutsideRootErrorDoesNotLeakHostPath 验证越界报错里只有调用方给的相对路径，
// 不含宿主机绝对路径。
func TestOutsideRootErrorDoesNotLeakHostPath(t *testing.T) {
	fs, _ := newEscaped(t)
	_, err := fs.Read(context.Background(), "link/secret.txt")
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("want ErrOutsideRoot, got %v", err)
	}
	if strings.Contains(err.Error(), fs.Root()) {
		t.Errorf("error leaks host path: %v", err)
	}
}

// TestSearchDoesNotEnumerateOutsideDir 验证搜索不会顺着目录链接把 root 之外的
// 目录树枚举出来（链接里的文件名不该出现在结果里）。
func TestSearchDoesNotEnumerateOutsideDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside-secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir(t, root, "link", outside)
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	results, err := fs.Search(context.Background(), "outside-secret", false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, e := range results {
		t.Errorf("search enumerated file outside root: %+v", e)
	}
}

// --- 沙盒：指向 root 内部的链接照常可用（不能一刀切禁掉链接） ---

func TestLinkInsideRootIsAllowed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "a.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir(t, root, "link", target)
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, err := fs.Read(context.Background(), "link/a.txt")
	if err != nil {
		t.Fatalf("Read through inner link: %v", err)
	}
	if string(data) != "inside" {
		t.Errorf("Read = %q, want %q", data, "inside")
	}

	page, err := fs.List(context.Background(), "link", 1, 20)
	if err != nil {
		t.Fatalf("List through inner link: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Name != "a.txt" {
		t.Errorf("List entries = %+v", page.Entries)
	}
}

// TestDeleteInnerLinkKeepsTarget 验证删掉指向 root 内部的链接时，只删链接本身，
// 链接目标不受影响。
func TestDeleteInnerLinkKeepsTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "a.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir(t, root, "link", target)
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := fs.Delete(context.Background(), "link", false); err != nil {
		t.Fatalf("Delete link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link")); !os.IsNotExist(err) {
		t.Errorf("link should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "a.txt")); err != nil {
		t.Fatalf("link target should survive: %v", err)
	}
}

// TestWriteThroughInnerLink 验证写穿过指向 root 内部的链接是允许的。
func TestWriteThroughInnerLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir(t, root, "link", target)
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := fs.Write(context.Background(), "link/a.txt", []byte("written")); err != nil {
		t.Fatalf("Write through inner link: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "a.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "written" {
		t.Errorf("file = %q, want %q", data, "written")
	}
}

// TestWriteNewFileThroughEscapingLink 验证"写新文件"这条路也不能顺着链接跑到
// root 外面去（链接指向外部时，写入同样被拒）。
func TestWriteNewFileThroughEscapingLink(t *testing.T) {
	fs, outside := newEscaped(t)
	if _, err := fs.Write(context.Background(), "link/new.txt", []byte("PWNED")); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("file created outside root, stat err = %v", err)
	}
}

// TestWriteNewFileInNewDirThroughEscapingLink 覆盖"父目录也要新建"的组合：
// 链接指向外部时，在链接下新建子目录/文件同样被拒。
func TestWriteNewFileInNewDirThroughEscapingLink(t *testing.T) {
	fs, outside := newEscaped(t)
	if _, err := fs.Write(context.Background(), "link/sub/new.txt", []byte("PWNED")); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); !os.IsNotExist(err) {
		t.Errorf("dir created outside root, stat err = %v", err)
	}
}

// TestRootItselfIsLink 验证 root 本身是链接时（Windows junction / Unix 软链）
// 依然正常工作——不能因为 root 解开了就误判所有路径越界。
func TestRootItselfIsLink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, real).CombinedOutput()
		if err != nil {
			t.Skipf("cannot create junction: %v (%s)", err, out)
		}
	} else if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	fs, err := New(link)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data, err := fs.Read(context.Background(), "a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("Read = %q, want %q", data, "hello")
	}
}
