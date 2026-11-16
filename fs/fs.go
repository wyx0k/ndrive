// Package fs 定义 ndrive 的存储契约。
//
// # 定位
//
// fs 是 workspace 的一等成员，不是某个 tool 的私有实现。当前架构（见 ARCHITECTURE.md）里：
//
//   - storage 只有一个底座，抽象成 FileService 接口；
//   - 具体实现由用户选择（当前只有 fs/local —— 本地目录）；
//   - 消费方（func、agent 运行时、UI）只依赖 FileService 接口，不依赖实现，
//     实现之间可以互换（依赖倒置）。
//
// # 约定目录
//
// 每个 workspace 的文件系统必须包含两个约定目录：
//
//   - TmpDir（.tmp）—— 临时产物：中间文件、尚未落定的草稿；
//   - MemDir（.mem）—— 长期记忆：跨会话保留的状态。
//
// 这两个目录由 FileService 的实现负责保证存在（实现方在初始化时创建），
// 上层可以直接假设它们可用，不必每次调用前先探查。
//
// # 路径约定
//
// 所有路径参数都是相对 root 的相对路径，用 "/" 分隔，不含前导 "/"。
// 空字符串表示 root 本身。具体实现负责把相对路径解析/校验到自己的存储根，
// 禁止越界访问。
//
// # 与 bus 的关系
//
// fs 不依赖 bus。文件系统变更事件通过可选的 Watcher 接口暴露（Go 原生
// channel），由根包桥接到 bus —— 存储层不该知道消息总线存在。
//
// 桥接后的 topic 是 TopicChange（"fs.change"），payload 是本包的 Event。
package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// 约定目录名。所有 FileService 实现都必须保证这两个目录存在。
const (
	// TmpDir 临时目录名（相对 root）。存放中间产物、可随时清理。
	TmpDir = ".tmp"

	// MemDir 记忆目录名（相对 root）。存放跨会话保留的长期状态。
	MemDir = ".mem"
)

// TopicChange 是文件变更事件在 bus 上的 topic。
//
// fs 包本身不依赖 bus，但 topic 名字是跨包约定，放在这里让发布方（根包的
// 桥接）和订阅方（UI / agent 运行时）用同一个常量。bus 不支持通配符订阅，
// 所以所有变更事件走同一个 topic，具体类型看 payload 里的 Op。
const TopicChange = "fs.change"

// Config 是文件系统配置的契约：配置对象负责把自己初始化成 FileService。
//
// 实现方（fs/local 等）提供自己的 Config 类型并实现 NewFileService；
// 根包只持有这个接口，不认识任何具体实现——新增后端不需要改根包。
type Config interface {
	// NewFileService 按配置初始化出一个 FileService。
	// 实现必须保证 TmpDir / MemDir 两个约定目录存在。
	NewFileService(ctx context.Context) (FileService, error)
}

// FileEntry 是目录条目 / 文件元信息的统一表示。
type FileEntry struct {
	// Name 是文件/目录的 base name（不含路径前缀）。
	Name string `json:"name"`
	// Path 是相对 root 的路径（使用 "/" 分隔，不含前导 "/"）。
	Path string `json:"path"`
	// IsDir 标识这是目录还是文件。
	IsDir bool `json:"is_dir"`
	// Size 是文件大小（字节）。IsDir 为 true 时为 0。
	Size int64 `json:"size"`
	// ModTime 是最后修改时间。
	ModTime time.Time `json:"mod_time"`
	// Brief 是内容简介，仅对 .md 文件非空（截取正文前 100 个字符）。
	Brief string `json:"brief,omitempty"`
}

// ListPage 是 List 的分页结果。
type ListPage struct {
	Entries    []FileEntry `json:"entries"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	Total      int         `json:"total"` // 当前目录下条目总数
	TotalPages int         `json:"total_pages"`
}

// FileService 是 ndrive 的存储契约。所有路径参数都是相对 root 的相对路径
// （"/" 分隔，不含前导 "/"）；具体实现负责把相对路径解析/校验到自己的存储根，
// 禁止越界访问。
//
// 实现必须保证 TmpDir / MemDir 两个约定目录存在。
type FileService interface {
	// List 列出 path 目录下的条目（仅当前层级，不递归），按 name 排序分页。
	// page 从 1 开始；pageSize <= 0 时使用实现方默认值。
	List(ctx context.Context, path string, page, pageSize int) (ListPage, error)

	// Stat 返回单个路径的元信息（不含 Brief，调用方需要简介请用 List 或 Read）。
	Stat(ctx context.Context, path string) (FileEntry, error)

	// Read 一次性读取整个文件内容。实现方可对文件大小设上限，
	// 超限返回 error（调用方应改用 Open 做流式读取）。
	Read(ctx context.Context, path string) ([]byte, error)

	// Open 以流式方式打开文件，返回的 io.ReadCloser 由调用方负责 Close。
	// 用于大文件 / 需要 io.Reader 语义的场景（如 zip 解析）。
	Open(ctx context.Context, path string) (io.ReadCloser, error)

	// Search 按文件名（或可选内容）搜索，返回匹配的条目（不分页，调用方自行限流）。
	// contentSearch 为 true 时额外对文本文件内容做子串匹配（更慢）。
	Search(ctx context.Context, query string, contentSearch bool) ([]FileEntry, error)

	// Write 创建或整体覆盖 path 的内容；父目录不存在时自动创建。
	Write(ctx context.Context, path string, body []byte) (FileEntry, error)

	// WriteStream 以流式方式把 r 的内容写入 path；父目录不存在时自动创建。
	// 用于大内容 / 不想先整体读入内存的场景（如其它 tool 转发一个 HTTP body）。
	WriteStream(ctx context.Context, path string, r io.Reader) (FileEntry, error)

	// Edit 对已存在文件做乐观并发的字符串替换：old 必须在当前内容中恰好出现
	// 一次，否则返回 error；expectedModTime 必须与文件当前 ModTime 一致，
	// 否则返回 ErrModTimeMismatch（说明文件在读取之后已被改过）。
	Edit(ctx context.Context, path string, old, newText string, expectedModTime time.Time) (FileEntry, error)

	// Delete 删除 path 指向的文件或目录。
	// path 指向目录时必须显式传 recursive=true 才允许递归删除整棵子树，
	// 否则实现方应拒绝（避免"删个文件"顺手删掉整棵目录树）。
	// 删除存储根目录本身一律拒绝（根是沙盒边界，删掉后整个存储就没了）。
	Delete(ctx context.Context, path string, recursive bool) error
}

// Op 是文件系统变更事件的类型。
type Op int

const (
	// OpCreated 新建。
	OpCreated Op = iota
	// OpModified 内容变更。
	OpModified
	// OpRemoved 删除。
	OpRemoved
	// OpRenamed 重命名。
	OpRenamed
)

// String 实现 fmt.Stringer。
func (o Op) String() string {
	switch o {
	case OpCreated:
		return "created"
	case OpModified:
		return "modified"
	case OpRemoved:
		return "removed"
	case OpRenamed:
		return "renamed"
	}
	return "unknown"
}

// MarshalJSON 把 Op 编成字符串（"created"/"modified"/…），事件过 bus 到 UI
// 时人眼可读，也便于跨语言消费。
func (o Op) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.String())
}

// UnmarshalJSON 解析 Op 字符串。
func (o *Op) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch s {
	case "created":
		*o = OpCreated
	case "modified":
		*o = OpModified
	case "removed":
		*o = OpRemoved
	case "renamed":
		*o = OpRenamed
	default:
		return fmt.Errorf("fs: unknown op %q", s)
	}
	return nil
}

// Event 是文件系统变更事件。
type Event struct {
	// Op 变更类型。
	Op Op `json:"op"`
	// Path 是相对 root 的路径（"/" 分隔，不含前导 "/"）。
	Path string `json:"path"`
	// IsDir 标识变更对象是否为目录。
	IsDir bool `json:"is_dir"`
	// Size 是文件大小（字节）。
	Size int64 `json:"size"`
	// ModTime 是最后修改时间。
	ModTime time.Time `json:"mod_time"`
}

// Watcher 是 FileService 的可选扩展能力：把文件系统变更暴露成事件流。
//
// 不是 FileService 的一部分 —— 存储实现可以不支持变更通知（只读后端、
// 单文件后端等），上层通过类型断言探测：
//
//	if w, ok := ws.FS().(fs.Watcher); ok { ... }
//
// fs 包不依赖 bus：事件走 Go 原生 channel，由根包桥接到 bus。
type Watcher interface {
	// Events 返回变更事件 channel。channel 由实现方负责关闭（Close 之后
	// 关闭 channel，接收方据此退出循环）。
	Events() <-chan Event

	// Close 停止监听并释放底层资源。重复调用安全。
	Close() error
}
