// Package local 是 ndrive 存储契约（fs.FileService）的一个实现：把 root
// 目录映射成一层受沙盒保护的文件系统。
//
// 它就是 fs-local —— 当前唯一的 FileService 实现。用户可以按同一接口实现
// 别的后端（内存、单文件、对象存储……），上层只依赖 fs.FileService 接口。
//
// # 生命周期
//
// New 负责把 root 准备好：目录不存在就创建，并保证 fs.TmpDir / fs.MemDir
// 两个约定目录存在。开启 WithWatch 后还会启动文件变更监听，用完必须调
// Close 释放。
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
package local

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
	"sync"
	"time"

	filewatcher "github.com/larsartmann/go-filewatcher/v2"

	"github.com/wyx0k/ndrive/fs"
)

// DefaultPageSize 是 List 未指定 pageSize 时的默认分页大小。
const DefaultPageSize = 20

// DefaultMaxReadSize 是 Read 允许一次性读取的最大文件大小（字节）。
// 超过此大小的文件请用 Open 做流式读取。
const DefaultMaxReadSize = 10 << 20 // 10 MiB

// DefaultWatchDebounce 是文件变更监听的默认合并窗口。
//
// 窗口内同一个路径的多个底层事件合并成一条（见 mergeEvent），窗口结束
// 才投递出去。
const DefaultWatchDebounce = 500 * time.Millisecond

// watchBufferSize 是事件转发 channel 的缓冲区大小。
const watchBufferSize = 64

// seenTTL 是"已报过创建"记录的保留时长。
//
// 这张表用来压掉重复的创建事件：新建目录补扫和底层 watcher 事件可能指向
// 同一个文件（谁先到不定），两条都得发出去的话，UI 会收到两次一样的变更。
const seenTTL = 30 * time.Second

// briefMaxChars 是 .md 文件 Brief 截取的最大字符数。
const briefMaxChars = 100

// ErrOutsideRoot 在路径解析越界到 root 之外时返回。
var ErrOutsideRoot = fmt.Errorf("local: path escapes root")

// ErrFileTooLarge 在 Read 的文件大小超过 MaxReadSize 时返回。
var ErrFileTooLarge = fmt.Errorf("local: file exceeds max read size")

// ErrModTimeMismatch 在 Edit 的 expectedModTime 与文件当前 ModTime 不一致时
// 返回，表示文件在调用方读取之后已被改过。
var ErrModTimeMismatch = fmt.Errorf("local: file mod time mismatch")

// ErrOldNotFound 在 Edit 的 old 子串在文件内容中一次都没出现时返回。
var ErrOldNotFound = fmt.Errorf("local: old text not found")

// ErrOldNotUnique 在 Edit 的 old 子串在文件内容中出现多次时返回（必须恰好一次）。
var ErrOldNotUnique = fmt.Errorf("local: old text is not unique")

// ErrDeleteRoot 在试图删除沙盒根目录本身时返回（root 是沙盒边界，不允许被删掉）。
var ErrDeleteRoot = fmt.Errorf("local: refusing to delete root")

// ErrIsDirectory 在删除目录但没有显式要求递归时返回。
var ErrIsDirectory = fmt.Errorf("local: path is a directory")

// FS 是 local 对 fs.FileService 的具体实现。
type FS struct {
	root string
	// realRoot 是 root 的真实路径（解开 root 自身可能存在的软链）。
	// 越界判定拿真实路径比，否则 root 是软链时（macOS 的 /tmp、挂载点）
	// 所有访问都会被误判成越界。
	realRoot    string
	maxReadSize int64

	// watcher 是文件变更监听器；未开启 WithWatch 时为 nil。
	watcher *filewatcher.Watcher
	// events 是变更事件 channel；未开启 WithWatch 时为 nil。
	events chan fs.Event
	// done 在 Close 时关闭，通知转发 goroutine 停止投递事件。
	done chan struct{}
	// closeOnce 保证 Close 幂等。
	closeOnce sync.Once

	// pending 是合并窗口内待投递的事件：路径 → 事件。
	// pendingOrder 记录投递顺序（目录在前、目录里的文件在后）。
	// 两个字段只由 watchLoop 一个 goroutine 访问，不需要加锁。
	pending      map[string]fs.Event
	pendingOrder []string
	// seen 记录已经报过"创建"的路径（路径 → 投递时间），用于压掉重复。
	seen map[string]time.Time

	// watchEnabled / watchDebounce 是 WithWatch 的配置，由 New 在构造后消费。
	watchEnabled  bool
	watchDebounce time.Duration
}

// Option 配置 FS 的可选行为。
type Option func(*FS)

// WithMaxReadSize 覆盖 Read 的文件大小上限（字节）。<= 0 表示不限制。
func WithMaxReadSize(n int64) Option {
	return func(f *FS) { f.maxReadSize = n }
}

// WithWatch 开启文件变更监听。debounce <= 0 时使用 DefaultWatchDebounce。
//
// debounce 是事件合并窗口：窗口内同一个路径的多个底层事件合并成一条，
// 窗口结束才投递出去。
//
// 开启后必须调 Close 释放底层资源（watcher goroutine）。
func WithWatch(debounce time.Duration) Option {
	return func(f *FS) {
		f.watchEnabled = true
		f.watchDebounce = debounce
	}
}

// New 构造一个以 root 为根目录的 FS。
//
// root 不存在会被自动创建；fs.TmpDir / fs.MemDir 两个约定目录也会被创建，
// 上层可以直接使用不必先探查。
func New(root string, opts ...Option) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("local: resolve root %q: %w", root, err)
	}
	// root 不存在就建 —— workspace 首次打开时 root 通常还不存在。
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("local: create root %q: %w", abs, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("local: stat root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local: root %q is not a directory", abs)
	}
	realRoot, err := evalLinks(abs)
	if err != nil {
		return nil, fmt.Errorf("local: resolve root symlinks %q: %w", abs, err)
	}
	f := &FS{
		root:        abs,
		realRoot:    realRoot,
		maxReadSize: DefaultMaxReadSize,
		done:        make(chan struct{}),
		events:      nil,
	}
	for _, opt := range opts {
		opt(f)
	}
	// 约定目录：每个 workspace 的文件系统必须包含这两个。
	for _, dir := range []string{fs.TmpDir, fs.MemDir} {
		if err := os.MkdirAll(filepath.Join(abs, dir), 0o755); err != nil {
			return nil, fmt.Errorf("local: create %s dir: %w", dir, err)
		}
	}
	if f.watchEnabled {
		if err := f.startWatch(); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// startWatch 启动文件变更监听，并把 filewatcher 事件转发到 f.events。
func (f *FS) startWatch() error {
	w, err := filewatcher.New([]string{f.root},
		// 合并窗口在本地做，不用库自带的防抖：库自带的要么是全局的（同一批
		// 里只保留最后一条，其它文件的事件全丢），要么按路径（保留最后一条，
		// 把"这是新建文件"的语义抹掉）。规则见 mergeEvent。
		//
		// 约定目录 /.tmp 与 /.mem 是点开头的目录，filewatcher 默认会跳过
		// 点目录 —— 必须显式关掉，否则工作区记忆目录里的变更永远不会发出去。
		filewatcher.WithSkipDotDirs(false),
	)
	if err != nil {
		return fmt.Errorf("local: create watcher: %w", err)
	}
	raw, err := w.Watch(context.Background())
	if err != nil {
		_ = w.Close()
		return fmt.Errorf("local: start watch: %w", err)
	}
	f.watcher = w
	f.events = make(chan fs.Event, watchBufferSize)
	f.pending = make(map[string]fs.Event)
	f.seen = make(map[string]time.Time)

	go f.watchLoop(raw)
	return nil
}

// watchLoop 是变更事件的唯一出口。
//
// 底层 watcher 的事件粒度比 fs.Event 细（一次写入可能是"创建 + 写入"两条），
// 而且会漏事件（新目录里"建好之后、监听挂上之前"写的文件它看不到）。
// 这里统一处理：按路径合并、补扫新目录，再按顺序投递。
func (f *FS) watchLoop(raw <-chan filewatcher.Event) {
	defer close(f.events)

	window := f.watchDebounce
	if window <= 0 {
		window = DefaultWatchDebounce
	}
	ticker := time.NewTicker(window)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-raw:
			if !ok {
				f.flush()
				return
			}
			f.handle(ev)
		case <-ticker.C:
			f.flush()
		case <-f.done:
			f.flush()
			return
		}
	}
}

// handle 处理一条底层 watcher 事件。
func (f *FS) handle(raw filewatcher.Event) {
	// 事件路径必须仍在 root 内 —— 否则 toRel 会算出带 ".." 的相对路径，
	// 把宿主机目录结构漏出去。
	if !f.contains(raw.Path) {
		return
	}
	e := f.toEvent(raw)
	f.stage(e)
	// 新目录：底层 watcher 是收到这个事件之后才把该目录挂上监听的，
	// 目录建好之后、监听挂上之前写进去的文件它永远看不到 —— 补扫一遍。
	if e.Op == fs.OpCreated && e.IsDir {
		f.collectSubtree(e.Path)
	}
}

// stage 把事件并进合并窗口。
//
// 同一窗口内同一路径的多个事件合并成一条（mergeEvent）；已经报过创建的
// 路径不再重复报创建（见 seen）。
func (f *FS) stage(e fs.Event) {
	switch e.Op {
	case fs.OpRemoved:
		// 文件没了 —— 清掉"已报过创建"的记录，之后重建时要重新报创建。
		delete(f.seen, e.Path)
	case fs.OpCreated:
		if at, ok := f.seen[e.Path]; ok && time.Since(at) < seenTTL {
			return
		}
	}
	if cur, ok := f.pending[e.Path]; ok {
		merged, keep := mergeEvent(cur, e)
		if !keep {
			// 窗口内建了又删 —— 净变化为零。
			delete(f.pending, e.Path)
			return
		}
		f.pending[e.Path] = merged
		return
	}
	f.pending[e.Path] = e
	f.pendingOrder = append(f.pendingOrder, e.Path)
}

// mergeEvent 合并窗口内同一路径的两个事件，返回合并结果和是否还值得上报
// （false = 净变化为零，不用报）。
//
// 优先级：创建 > 删除 > 修改。底层 watcher 对新文件先报"创建"再报"写入"，
// 合并时必须保住"创建"这个语义 —— 否则上层分不清"新建的文件"和"被改过的
// 已有文件"。
func mergeEvent(cur, next fs.Event) (fs.Event, bool) {
	switch {
	case cur.Op == fs.OpCreated && next.Op == fs.OpRemoved:
		// 窗口内建了又删。
		return fs.Event{}, false
	case cur.Op == fs.OpRemoved && next.Op == fs.OpCreated:
		// 删了又建 —— 文件还在，等于重新创建。
		return next, true
	case cur.Op == fs.OpRemoved:
		return cur, true
	case next.Op == fs.OpRemoved:
		return next, true
	case cur.Op == fs.OpCreated || next.Op == fs.OpCreated:
		return fs.Event{
			Op:      fs.OpCreated,
			Path:    cur.Path,
			IsDir:   cur.IsDir || next.IsDir,
			Size:    next.Size,
			ModTime: next.ModTime,
		}, true
	default:
		// 修改 + 修改。
		return next, true
	}
}

// flush 把窗口内攒下的事件按顺序投递出去。
func (f *FS) flush() {
	for _, path := range f.pendingOrder {
		e, ok := f.pending[path]
		if !ok {
			continue
		}
		delete(f.pending, path)
		if e.Op == fs.OpCreated {
			f.seen[path] = time.Now()
		}
		f.deliver(e)
	}
	f.pendingOrder = f.pendingOrder[:0]
	f.pruneSeen()
}

// pruneSeen 清理过期的"已报过创建"记录，避免无限增长。
func (f *FS) pruneSeen() {
	if len(f.seen) < 256 {
		return
	}
	for path, at := range f.seen {
		if time.Since(at) > seenTTL {
			delete(f.seen, path)
		}
	}
}

// deliver 投递一条事件。消费者不消费时这里阻塞，直到 Close。
func (f *FS) deliver(e fs.Event) {
	select {
	case f.events <- e:
	case <-f.done:
	}
}

// collectSubtree 补扫新建目录下的文件，补成"创建"事件。
//
// 底层 watcher 是收到"目录创建"事件之后才把该目录挂上监听的，所以"目录
// 建好之后、监听挂上之前"写进去的文件它永远看不到 —— fs.Write 里的
// "建目录 + 写文件"是挨着执行的，正好落在这个窗口里。这里把整棵新目录
// 扫一遍，补成创建事件。
func (f *FS) collectSubtree(relDir string) {
	abs := filepath.Join(f.root, filepath.FromSlash(relDir))
	_ = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == abs {
			return nil //nolint:nilerr // 读不到就跳过，不影响其它条目
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil //nolint:nilerr // 同上
		}
		f.stage(fs.Event{
			Op:      fs.OpCreated,
			Path:    f.toRel(path),
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		return nil
	})
}

// toEvent 把 filewatcher.Event 转成 fs.Event。路径转换为相对 root 的沙盒
// 路径，不泄露宿主机绝对路径。
func (f *FS) toEvent(ev filewatcher.Event) fs.Event {
	e := fs.Event{
		Path:    f.toRel(ev.Path),
		IsDir:   ev.IsDir,
		Size:    ev.Size,
		ModTime: ev.ModTime,
	}
	switch ev.Op {
	case filewatcher.Create:
		e.Op = fs.OpCreated
	case filewatcher.Write:
		e.Op = fs.OpModified
	case filewatcher.Remove:
		e.Op = fs.OpRemoved
	case filewatcher.Rename:
		e.Op = fs.OpRenamed
	}
	return e
}

// Events 返回文件变更事件 channel。
// 未开启 WithWatch 时返回一个已关闭的 channel —— 接收方的 range 循环会
// 立即结束，不会挂住。
func (f *FS) Events() <-chan fs.Event {
	if f.events == nil {
		ch := make(chan fs.Event)
		close(ch)
		return ch
	}
	return f.events
}

// Close 停止文件变更监听并释放底层资源。重复调用安全。
// 未开启 WithWatch 时是空操作。
func (f *FS) Close() error {
	var err error
	f.closeOnce.Do(func() {
		close(f.done)
		if f.watcher != nil {
			err = f.watcher.Close()
			f.watcher = nil
		}
	})
	return err
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
		return "", fmt.Errorf("local: resolve %q: %w", relPath, err)
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
		return fmt.Errorf("local: resolve path: %w", err)
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
	return "", fmt.Errorf("local: too many levels of symbolic links: %q", abs)
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
func (f *FS) entryFromInfo(abs string, info os.FileInfo) fs.FileEntry {
	return fs.FileEntry{
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
func (f *FS) List(_ context.Context, path string, page, pageSize int) (fs.ListPage, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return fs.ListPage{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fs.ListPage{}, fmt.Errorf("local: stat %q: %w", path, err)
	}
	if !info.IsDir() {
		return fs.ListPage{}, fmt.Errorf("local: %q is not a directory", path)
	}

	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return fs.ListPage{}, fmt.Errorf("local: read dir %q: %w", path, err)
	}

	entries := make([]fs.FileEntry, 0, len(dirEntries))
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

	return fs.ListPage{
		Entries:    entries[start:end],
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

// Stat 返回单个路径的元信息。
func (f *FS) Stat(_ context.Context, path string) (fs.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return fs.FileEntry{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: stat %q: %w", path, err)
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
		return nil, fmt.Errorf("local: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("local: %q is a directory", path)
	}
	if f.maxReadSize > 0 && info.Size() > f.maxReadSize {
		return nil, fmt.Errorf("%w: %q size=%d max=%d", ErrFileTooLarge, path, info.Size(), f.maxReadSize)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("local: read %q: %w", path, err)
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
		return nil, fmt.Errorf("local: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("local: %q is a directory", path)
	}
	fh, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("local: open %q: %w", path, err)
	}
	return fh, nil
}

// Write 创建或整体覆盖 path 的内容；父目录不存在时自动创建。
func (f *FS) Write(_ context.Context, path string, body []byte) (fs.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return fs.FileEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: mkdir for %q: %w", path, err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: write %q: %w", path, err)
	}
	return f.Stat(context.Background(), path)
}

// WriteStream 以流式方式把 r 的内容写入 path；父目录不存在时自动创建。
func (f *FS) WriteStream(_ context.Context, path string, r io.Reader) (fs.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return fs.FileEntry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: mkdir for %q: %w", path, err)
	}
	fh, err := os.Create(abs)
	if err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: create %q: %w", path, err)
	}
	_, copyErr := io.Copy(fh, r)
	closeErr := fh.Close()
	if copyErr != nil {
		return fs.FileEntry{}, fmt.Errorf("local: write stream %q: %w", path, copyErr)
	}
	if closeErr != nil {
		return fs.FileEntry{}, fmt.Errorf("local: close %q: %w", path, closeErr)
	}
	return f.Stat(context.Background(), path)
}

// Edit 对已存在文件做乐观并发的字符串替换。expectedModTime 必须与文件当前
// ModTime 一致，否则返回 ErrModTimeMismatch；old 必须在内容中恰好出现一次，
// 否则返回 ErrOldNotFound / ErrOldNotUnique。
func (f *FS) Edit(_ context.Context, path string, old, newText string, expectedModTime time.Time) (fs.FileEntry, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return fs.FileEntry{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return fs.FileEntry{}, fmt.Errorf("local: %q is a directory", path)
	}
	if !info.ModTime().Equal(expectedModTime) {
		return fs.FileEntry{}, fmt.Errorf("%w: %q current=%s expected=%s", ErrModTimeMismatch, path, info.ModTime(), expectedModTime)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: read %q: %w", path, err)
	}
	content := string(data)
	switch strings.Count(content, old) {
	case 0:
		return fs.FileEntry{}, fmt.Errorf("%w: %q in %q", ErrOldNotFound, old, path)
	case 1:
		// 恰好一次，继续替换。
	default:
		return fs.FileEntry{}, fmt.Errorf("%w: %q in %q", ErrOldNotUnique, old, path)
	}

	updated := strings.Replace(content, old, newText, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return fs.FileEntry{}, fmt.Errorf("local: write %q: %w", path, err)
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
		return fmt.Errorf("local: stat %q: %w", path, err)
	}
	if info.IsDir() {
		if !recursive {
			return fmt.Errorf("%w: %q (recursive=true required to delete a directory)", ErrIsDirectory, path)
		}
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("local: remove dir %q: %w", path, err)
		}
		return nil
	}
	if err := os.Remove(abs); err != nil {
		return fmt.Errorf("local: remove %q: %w", path, err)
	}
	return nil
}

// Search 递归遍历 root，按文件名（及可选内容）子串匹配 query（大小写不敏感）。
func (f *FS) Search(_ context.Context, query string, contentSearch bool) ([]fs.FileEntry, error) {
	if query == "" {
		return nil, fmt.Errorf("local: search query is empty")
	}
	q := strings.ToLower(query)
	var results []fs.FileEntry

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
		return nil, fmt.Errorf("local: search: %w", err)
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

var _ fs.FileService = (*FS)(nil)
var _ fs.Watcher = (*FS)(nil)
