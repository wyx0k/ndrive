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
//
// 同时实现 tools.ServicePlugin（Service() 返回 builtin.FileService，
// 供其他 plugin 原生流式调用 Open，不走 ToolsAPI.Call）和
// tools.StatefulPlugin（后台监听文件系统变更，发 file.* 事件到 bus）。
//
// 所有路径参数都是相对 root 的相对路径；内部做沙盒校验，禁止通过 ".." 等
// 方式越界访问 root 之外的文件。
package localfs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// FS 是 localfs 对 builtin.FileService 的具体实现。
type FS struct {
	root        string
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
	f := &FS{root: abs, maxReadSize: DefaultMaxReadSize}
	for _, opt := range opts {
		opt(f)
	}
	return f, nil
}

// Root 返回绝对根目录路径。
func (f *FS) Root() string { return f.root }

// resolve 把相对路径解析为绝对路径，并校验结果仍在 root 内。
// 空字符串 / "." 代表根目录本身；任何经 ".." 等方式越界到 root 之外的
// 路径都直接拒绝（不做静默裁剪），避免 "/../x" 这种写法被 Clean 吸收成
// root 内路径而绕过沙盒校验。
func (f *FS) resolve(relPath string) (string, error) {
	cleaned := filepath.Clean(filepath.ToSlash(relPath))
	abs := filepath.Join(f.root, cleaned)

	rel, err := filepath.Rel(f.root, abs)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, relPath)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrOutsideRoot, relPath)
	}
	return abs, nil
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
func readBrief(abs string) string {
	if !strings.EqualFold(filepath.Ext(abs), ".md") {
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
			entry.Brief = readBrief(childAbs)
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
		nameMatch := strings.Contains(strings.ToLower(d.Name()), q)
		if nameMatch {
			info, err := d.Info()
			if err != nil {
				return nil //nolint: nilerr // best-effort: 跳过读不到信息的条目
			}
			entry := f.entryFromInfo(path, info)
			if !entry.IsDir {
				entry.Brief = readBrief(path)
			}
			results = append(results, entry)
			return nil
		}
		if !contentSearch || d.IsDir() {
			return nil
		}
		if matchesContent(path, q) {
			info, err := d.Info()
			if err != nil {
				return nil //nolint: nilerr
			}
			entry := f.entryFromInfo(path, info)
			entry.Brief = readBrief(path)
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
func matchesContent(path string, lowerQuery string) bool {
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
