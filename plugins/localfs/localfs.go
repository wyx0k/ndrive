// Package localfs 提供一个基于本地文件系统的 ndrive plugin。
//
// 能力：
//   - store.localfs.list          —— 目录树分页 list（.md 文件带前 100 字简介）
//   - store.localfs.read          —— 一次性读取整文件（带大小上限）
//   - store.localfs.search        —— 文件名 / 可选内容搜索
//   - store.localfs.stat          —— 单路径元信息
//   - store.localfs.write         —— 创建或整体覆盖文件内容
//   - store.localfs.write-stream  —— 内部 tool，流式写入（供其它 plugin 原生调用）
//   - store.localfs.write-markdown —— 写 markdown，可选带 YAML frontmatter
//   - store.localfs.edit          —— 乐观并发的 old/new 字符串替换
//   - store.localfs.delete        —— 删除文件/目录（目录需显式 recursive=true）
//
// 同时实现 tools.ServicePlugin（Service() 返回 builtin.FileService，
// 供其他 plugin 原生流式调用 Open，不走 ToolsAPI.Call）和
// tools.StatefulPlugin（后台监听文件系统变更，发 file.* 事件到 bus）。
//
// # 沙盒
//
// 所有路径参数都是相对 root 的相对路径。每个路径都要过两层校验（见
// FS.resolve）：
//
//  1. 词法校验 —— clean 之后必须仍在 root 内，拒绝 ".." 之类越界写法；
//  2. 链接校验 —— 解开路径里的软链 / junction 之后，真实路径必须仍在 root 内。
//     只做词法校验是不够的：root 里放一个指向 root 之外的链接，"link/x" 就能
//     读到沙盒外的文件，沙盒形同虚设。
//
// 凡是"读内容"的路径（readBrief / matchesContent）也各自再校验一次——目录
// 条目的中间路径由所在目录校验过，但末位可能是指向 root 之外的链接。
package localfs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/wyx0k/ndrive/tools/builtin"
)

// DefaultPageSize 是 List 未指定 pageSize 时的默认分页大小。
const DefaultPageSize = 20

// DefaultMaxReadSize 是 Read 允许一次性读取的最大文件大小（字节）。
// 超过此大小的文件请用 Open 做流式读取。
const DefaultMaxReadSize = 10 << 20 // 10 MiB

// briefMaxChars 是 .md 文件 Brief 截取的最大字符数。
const briefMaxChars = 100

// ErrOutsideRoot 在路径解析越界到 root 之外时返回。
var ErrOutsideRoot = fmt.Errorf("localfs: path escapes root")

// ErrFileTooLarge 在 Read 的文件大小超过 MaxReadSize 时返回。
var ErrFileTooLarge = fmt.Errorf("localfs: file exceeds max read size")

// ErrModTimeMismatch 在 Edit 的 expectedModTime 与文件当前 ModTime 不一致时
// 返回，表示文件在调用方读取之后已被改过。
var ErrModTimeMismatch = fmt.Errorf("localfs: file mod time mismatch")

// ErrOldNotFound 在 Edit 的 old 子串在文件内容中一次都没出现时返回。
var ErrOldNotFound = fmt.Errorf("localfs: old text not found")

// ErrOldNotUnique 在 Edit 的 old 子串在文件内容中出现多次时返回（必须恰好一次）。
var ErrOldNotUnique = fmt.Errorf("localfs: old text is not unique")

// ErrDeleteRoot 在试图删除沙盒根目录本身时返回（root 是沙盒边界，不允许被删掉）。
var ErrDeleteRoot = fmt.Errorf("localfs: refusing to delete root")

// ErrIsDirectory 在删除目录但没有显式要求递归时返回。
var ErrIsDirectory = fmt.Errorf("localfs: path is a directory")

// FS 是 localfs 对 builtin.FileService 的具体实现。
type FS struct {
	root string
	// realRoot 是 root 的真实路径（解开 root 自身可能存在的软链）。
	// 越界判定拿真实路径比，否则 root 是软链时（macOS 的 /tmp、挂载点）
	// 所有访问都会被误判成越界。
	realRoot    string
	maxReadSize int64
}

// Option 配置 FS 的可选行为。
type Option func(*FS)

// WithMaxReadSize 覆盖 Read 的文件大小上限（字节）。<= 0 表示不限制。
func WithMaxReadSize(n int64) Option {
	return func(f *FS) { f.maxReadSize = n }
}

// New 构造一个以 root 为根目录的 FS。root 必须是已存在的目录。
func New(root string, opts ...Option) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("localfs: resolve root %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("localfs: stat root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("localfs: root %q is not a directory", abs)
	}
	realRoot, err := evalLinks(abs)
	if err != nil {
		return nil, fmt.Errorf("localfs: resolve root symlinks %q: %w", abs, err)
	}
	f := &FS{root: abs, realRoot: realRoot, maxReadSize: DefaultMaxReadSize}
	for _, opt := range opts {
		opt(f)
	}
	return f, nil
}

// Root 返回绝对根目录路径。
func (f *FS) Root() string { return f.root }

// contains 判断 abs 是否位于 root 之内（词法判断，不解链接）。
// 供 watcher 事件过滤用：事件路径不在 root 内就不该发到 bus 上。
func (f *FS) contains(abs string) bool {
	return withinRoot(f.root, abs)
}

// resolve 把相对路径解析为绝对路径，并做两层安全校验：
//
//  1. 词法校验：clean 之后的结果必须仍在 root 内。空字符串 / "." 代表根
//     目录本身；任何经 ".." 等方式越界到 root 之外的路径都直接拒绝（不做
//     静默裁剪），避免 "/../x" 这种写法被 Clean 吸收成 root 内路径而绕过
//     沙盒校验。
//  2. 软链校验：把路径中已存在的部分解开软链后，真实路径必须仍在 root 内。
//     只做词法校验是不够的 —— root 里放一个指向 root 之外的软链，就能让
//     "link/passwd" 之类的路径读到沙盒外的文件，沙盒形同虚设。
//
// 目标不存在时（写新文件）按"已存在的最深祖先"校验，剩余段原样拼上。
//
// 已知残留风险：校验与实际 open 之间存在 TOCTOU 窗口——有本地写权限的
// 攻击者可以在窗口内把路径换成软链。要彻底消除需要 O_NOFOLLOW 之类的
// 打开语义，跨平台不可得，这里只保证"稳态下不越界"。
func (f *FS) resolve(relPath string) (string, error) {
	cleaned := filepath.Clean(filepath.ToSlash(relPath))
	abs := filepath.Join(f.root, cleaned)

	if !withinRoot(f.root, abs) {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, relPath)
	}
	if err := f.checkRealPath(abs); err != nil {
		if errors.Is(err, ErrOutsideRoot) {
			// 用调用方给的相对路径报错，不把宿主机绝对路径漏出去。
			return "", fmt.Errorf("%w: %q", ErrOutsideRoot, relPath)
		}
		return "", fmt.Errorf("localfs: resolve %q: %w", relPath, err)
	}
	return abs, nil
}

// checkRealPath 校验 abs 的真实路径（解开软链后）仍在 root 内。
// 用于两处：resolve 的第二层校验；以及目录条目（List/Search 的子项）读取
// 内容之前 —— 条目的中间路径由所在目录的 resolve 校验过，但末位可能是
// 指向 root 之外的软链，读它的内容会把沙盒外的数据带出来。
func (f *FS) checkRealPath(abs string) error {
	real, err := realPath(abs)
	if err != nil {
		return fmt.Errorf("localfs: resolve path: %w", err)
	}
	if !withinRoot(f.realRoot, real) {
		return ErrOutsideRoot
	}
	return nil
}

// withinRoot 判断 abs 是否位于 root 之内。
// Windows 上文件系统默认大小写不敏感，比较前先折叠大小写（per-dir 大小写
// 敏感开关罕见，忽略）。
func withinRoot(root, abs string) bool {
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		abs = strings.ToLower(abs)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		// 卷不同（不同盘符）——一定不在 root 内。
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// maxLinkDepth 限制解链接的最大层数，防止链接成环（link -> .. -> link）
// 时无限展开。
const maxLinkDepth = 40

// evalLinks 把 abs 逐段解析成真实路径：每一段若是链接（Unix 软链 / Windows
// 软链或 junction），就替换成链接目标再继续往下走。
//
// 刻意不用 filepath.EvalSymlinks：它在 Windows 上不认 junction ——
// os.Lstat 对 junction 不置 ModeSymlink 标记，EvalSymlinks 因此原样返回链接
// 路径。而 junction 恰恰是 Windows 上最常见的"目录指向别处"手法，不解析它等
// 于沙盒没设防。这里改用 os.Readlink 判定：Unix 软链、Windows 软链和 junction
// 都能读出目标；普通文件/目录读链接会报错，自然被当成"不是链接"。
func evalLinks(abs string) (string, error) {
	cur := abs
	for depth := 0; depth < maxLinkDepth; depth++ {
		next, changed, err := evalLinksOnce(cur)
		if err != nil {
			return "", err
		}
		if !changed {
			return next, nil
		}
		cur = next
	}
	return "", fmt.Errorf("localfs: too many levels of symbolic links: %q", abs)
}

// evalLinksOnce 把路径中最靠前的那个链接替换成它的目标，返回 (新路径, 是否发生
// 替换)。整条路径都不是链接时返回 (abs, false, nil)。
func evalLinksOnce(abs string) (string, bool, error) {
	vol := filepath.VolumeName(abs)
	rest := strings.TrimLeft(abs[len(vol):], string(filepath.Separator))

	cur := vol
	if cur != "" {
		cur += string(filepath.Separator)
	}
	parts := strings.Split(rest, string(filepath.Separator))
	for i, part := range parts {
		if part == "" {
			continue
		}
		next := filepath.Join(cur, part)
		target, err := os.Readlink(next)
		if err != nil {
			// 不是链接（普通文件/目录/不存在）——继续往下走。
			cur = next
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(cur, target)
		}
		tail := ""
		if i+1 < len(parts) {
			tail = filepath.Join(parts[i+1:]...)
		}
		if tail == "" {
			return target, true, nil
		}
		return filepath.Join(target, tail), true, nil
	}
	return abs, false, nil
}

// isLink 判断 path 是不是链接（Unix 软链 / Windows 软链或 junction）。
// 不用 FileInfo.Mode()&ModeSymlink：Windows 的 junction 不带 ModeSymlink
// 标记，只有 Readlink 能认出来。
func isLink(path string) bool {
	_, err := os.Readlink(path)
	return err == nil
}

// realPath 返回 abs 的真实路径：把路径中已存在的部分解开链接（软链 / Windows
// junction），尚未存在的部分（写新文件时的父目录/文件名）原样拼在已解析部分
// 之后。
func realPath(abs string) (string, error) {
	rest := ""
	cur := abs
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break // 已到文件系统根，再往上没有意义
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	real, err := evalLinks(cur)
	if err != nil {
		return "", err
	}
	if rest == "" {
		return real, nil
	}
	return filepath.Join(real, rest), nil
}

// toRel 把绝对路径转换回相对 root 的 "/" 分隔相对路径。
func (f *FS) toRel(abs string) string {
	rel, err := filepath.Rel(f.root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// entryFromInfo 构造 FileEntry；仅 list/stat 场景调用，不读取 Brief（由调用方决定是否补）。
func (f *FS) entryFromInfo(abs string, info os.FileInfo) builtin.FileEntry {
	return builtin.FileEntry{
		Name:    info.Name(),
		Path:    f.toRel(abs),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
}

// readBrief 读取 .md 文件正文前 briefMaxChars 个字符（按 rune 计数）。
// 任何读取失败都返回空字符串（简介是展示性信息，不影响主流程）。
// 读之前先做软链校验：指向 root 之外的链接不读，否则软链会把沙盒外的
// 文件内容带进 list/search 的结果里。
func (f *FS) readBrief(abs string) string {
	if !strings.EqualFold(filepath.Ext(abs), ".md") {
		return ""
	}
	if err := f.checkRealPath(abs); err != nil {
		return ""
	}
	fh, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer fh.Close()

	var sb strings.Builder
	count := 0
	reader := bufio.NewReader(fh)
	for count < briefMaxChars {
		r, _, err := reader.ReadRune()
		if err != nil {
			break
		}
		sb.WriteRune(r)
		count++
	}
	return sb.String()
}

// List 列出 path 目录下的条目（仅当前层级），按 name 排序分页。
func (f *FS) List(_ context.Context, path string, page, pageSize int) (builtin.ListPage, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return builtin.ListPage{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return builtin.ListPage{}, fmt.Errorf("localfs: stat %q: %w", path, err)
	}
	if !info.IsDir() {
		return builtin.ListPage{}, fmt.Errorf("localfs: %q is not a directory", path)
	}

	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return builtin.ListPage{}, fmt.Errorf("localfs: read dir %q: %w", path, err)
	}

	entries := make([]builtin.FileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		childAbs := filepath.Join(abs, de.Name())
		childInfo, err := de.Info()
		if err != nil {
			continue
		}
		entry := f.entryFromInfo(childAbs, childInfo)
		if !entry.IsDir {
			entry.Brief = f.readBrief(childAbs)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if page <= 0 {
		page = 1
	}
	total := len(entries)
	totalPages := (total + pageSize - 1) / pageSize
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return builtin.ListPage{
		Entries:    entries[start:end],
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

// Stat 返回单个路径的元信息。
func (f *FS) Stat(_ context.Context, path string) (builtin.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return builtin.FileEntry{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: stat %q: %w", path, err)
	}
	return f.entryFromInfo(abs, info), nil
}

// Read 一次性读取整个文件内容；超过 maxReadSize 返回 ErrFileTooLarge。
func (f *FS) Read(_ context.Context, path string) ([]byte, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("localfs: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("localfs: %q is a directory", path)
	}
	if f.maxReadSize > 0 && info.Size() > f.maxReadSize {
		return nil, fmt.Errorf("%w: %q size=%d max=%d", ErrFileTooLarge, path, info.Size(), f.maxReadSize)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("localfs: read %q: %w", path, err)
	}
	return data, nil
}

// Open 以流式方式打开文件；调用方负责 Close 返回的 ReadCloser。
func (f *FS) Open(_ context.Context, path string) (io.ReadCloser, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("localfs: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("localfs: %q is a directory", path)
	}
	fh, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("localfs: open %q: %w", path, err)
	}
	return fh, nil
}

// Write 创建或整体覆盖 path 的内容；父目录不存在时自动创建。
func (f *FS) Write(_ context.Context, path string, body []byte) (builtin.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return builtin.FileEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: mkdir for %q: %w", path, err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: write %q: %w", path, err)
	}
	return f.Stat(context.Background(), path)
}

// WriteStream 以流式方式把 r 的内容写入 path；父目录不存在时自动创建。
func (f *FS) WriteStream(_ context.Context, path string, r io.Reader) (builtin.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return builtin.FileEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: mkdir for %q: %w", path, err)
	}
	fh, err := os.Create(abs)
	if err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: create %q: %w", path, err)
	}
	_, copyErr := io.Copy(fh, r)
	closeErr := fh.Close()
	if copyErr != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: write stream %q: %w", path, copyErr)
	}
	if closeErr != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: close %q: %w", path, closeErr)
	}
	return f.Stat(context.Background(), path)
}

// Edit 对已存在文件做乐观并发的字符串替换。expectedModTime 必须与文件当前
// ModTime 一致，否则返回 ErrModTimeMismatch；old 必须在内容中恰好出现一次，
// 否则返回 ErrOldNotFound / ErrOldNotUnique。
func (f *FS) Edit(_ context.Context, path string, old, newText string, expectedModTime time.Time) (builtin.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return builtin.FileEntry{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return builtin.FileEntry{}, fmt.Errorf("localfs: %q is a directory", path)
	}
	if !info.ModTime().Equal(expectedModTime) {
		return builtin.FileEntry{}, fmt.Errorf("%w: %q current=%s expected=%s", ErrModTimeMismatch, path, info.ModTime(), expectedModTime)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: read %q: %w", path, err)
	}
	content := string(data)
	switch strings.Count(content, old) {
	case 0:
		return builtin.FileEntry{}, fmt.Errorf("%w: %q in %q", ErrOldNotFound, old, path)
	case 1:
		// 恰好一次，继续替换。
	default:
		return builtin.FileEntry{}, fmt.Errorf("%w: %q in %q", ErrOldNotUnique, old, path)
	}

	updated := strings.Replace(content, old, newText, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return builtin.FileEntry{}, fmt.Errorf("localfs: write %q: %w", path, err)
	}
	return f.Stat(context.Background(), path)
}

// Delete 删除 path 指向的文件或目录。
//
// 三条硬约束（防止"删一个文件"顺手把整个沙盒删掉）：
//   - path 指向沙盒根目录本身（空路径 / "."）时返回 ErrDeleteRoot；
//   - path 指向目录时必须显式传 recursive=true，否则返回 ErrIsDirectory
//     （不默认递归删除，避免误删整棵子树）；
//   - 路径越界（".." 等）由 resolve 拒绝，返回 ErrOutsideRoot。
//
// 用 Lstat 而不是 Stat：指向目录的符号链接按链接本身处理（删链接，不动目标）。
func (f *FS) Delete(_ context.Context, path string, recursive bool) error {
	abs, err := f.resolve(path)
	if err != nil {
		return err
	}
	if abs == f.root {
		return fmt.Errorf("%w: %q", ErrDeleteRoot, path)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return fmt.Errorf("localfs: stat %q: %w", path, err)
	}
	if info.IsDir() {
		if !recursive {
			return fmt.Errorf("%w: %q (recursive=true required to delete a directory)", ErrIsDirectory, path)
		}
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("localfs: remove dir %q: %w", path, err)
		}
		return nil
	}
	if err := os.Remove(abs); err != nil {
		return fmt.Errorf("localfs: remove %q: %w", path, err)
	}
	return nil
}

// Search 递归遍历 root，按文件名（及可选内容）子串匹配 query（大小写不敏感）。
func (f *FS) Search(_ context.Context, query string, contentSearch bool) ([]builtin.FileEntry, error) {
	if query == "" {
		return nil, fmt.Errorf("localfs: search query is empty")
	}
	q := strings.ToLower(query)
	var results []builtin.FileEntry

	err := filepath.WalkDir(f.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == f.root {
			return nil
		}
		// 目录链接（Unix 软链 / Windows junction）不进入：跟着它递归会把
		// root 之外的目录树枚举出来。链接本身仍可按名字匹配到（只报条目
		// 自身的元信息，不读它的内容）。
		if d.IsDir() && isLink(path) {
			return filepath.SkipDir
		}
		nameMatch := strings.Contains(strings.ToLower(d.Name()), q)
		if nameMatch {
			info, err := d.Info()
			if err != nil {
				return nil //nolint: nilerr // best-effort: 跳过读不到信息的条目
			}
			entry := f.entryFromInfo(path, info)
			if !entry.IsDir {
				entry.Brief = f.readBrief(path)
			}
			results = append(results, entry)
			return nil
		}
		if !contentSearch || d.IsDir() {
			return nil
		}
		if f.matchesContent(path, q) {
			info, err := d.Info()
			if err != nil {
				return nil //nolint: nilerr
			}
			entry := f.entryFromInfo(path, info)
			entry.Brief = f.readBrief(path)
			results = append(results, entry)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("localfs: search: %w", err)
	}
	return results, nil
}

// matchesContent 检查文件内容是否包含 query（已转小写）；任何读取失败都视为不匹配。
// 读之前先做软链校验：指向 root 之外的链接不读（内容搜索不能顺着软链把
// 沙盒外的文件内容搜出来）。
func (f *FS) matchesContent(path string, lowerQuery string) bool {
	if err := f.checkRealPath(path); err != nil {
		return false
	}
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()

	scanner := bufio.NewScanner(fh)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if strings.Contains(strings.ToLower(scanner.Text()), lowerQuery) {
			return true
		}
	}
	return false
}

var _ builtin.FileService = (*FS)(nil)
