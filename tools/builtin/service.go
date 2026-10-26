// FileService 是文件类插件的标准契约，定义在 builtin 让不同实现
// （本地文件系统、对象存储……）可以互相替换，消费方只认这一个接口类型。
//
// 提供方（如 plugins/localfs）实现 tools.ServicePlugin，Service() 返回
// 一个 FileService 值；消费方（如解析 pptx/docx 的 plugin）通过
// tools.DependentPlugin.DependsOn() 声明依赖该 plugin id，运行时用
// ws.API().Service(id) 取出后断言成 FileService 使用。
package builtin

import (
	"context"
	"io"
	"time"
)

// FileEntry 是目录条目 / 文件元信息的统一表示。
type FileEntry struct {
	// Name 是文件/目录的 base name（不含路径前缀）。
	Name string `json:"name"`
	// Path 是相对 FileService 根目录的路径（使用 "/" 分隔，不含前导 "/"）。
	Path string `json:"path"`
	// IsDir 标识这是目录还是文件。
	IsDir bool `json:"is_dir"`
	// Size 是文件大小（字节）；IsDir 为 true 时为 0。
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
	Total      int         `json:"total"`      // 当前目录下条目总数
	TotalPages int         `json:"total_pages"`
}

// FileService 是文件类插件对外暴露的原生能力接口。
// 所有路径参数都是相对 FileService 根目录的相对路径（使用 "/" 分隔）；
// 具体实现负责把相对路径解析/校验到自己的存储根，禁止越界访问。
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
	// 用于大内容 / 不想先整体读入内存的场景（如其它 plugin 转发一个 HTTP body）。
	WriteStream(ctx context.Context, path string, r io.Reader) (FileEntry, error)

	// Edit 对已存在文件做乐观并发的字符串替换：old 必须在当前内容中恰好出现
	// 一次，否则返回 error；expectedModTime 必须与文件当前 ModTime 一致，
	// 否则返回 ErrModTimeMismatch（说明文件在读取之后被改过）。
	Edit(ctx context.Context, path string, old, newText string, expectedModTime time.Time) (FileEntry, error)
}
