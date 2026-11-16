// config.go：local 文件系统的配置对象。
//
// 配置自己负责初始化：NewFileService 内部完成 root 准备、约定目录创建、
// 监听器启动——上层（根包）只持有 fs.Config 接口，不认识 local 的细节。
// 新增一个文件后端时同理：写一份自己的 Config 实现 fs.Config 即可，
// 不用改根包。
package local

import (
	"context"
	"time"

	"github.com/wyx0k/ndrive/fs"
)

// Config 是 local 文件系统的配置，实现 fs.Config。
type Config struct {
	// Path 工作区根目录。目录不存在会被自动创建，并连带建好 /.tmp 与 /.mem。
	Path string

	// Watch 开启文件变更监听。开启后变更事件经根包桥接到 bus
	//（topic fs.change），上层直接订阅即可。
	Watch bool

	// WatchDebounce 是文件变更监听的防抖间隔；<= 0 时用 DefaultWatchDebounce。
	WatchDebounce time.Duration

	// MaxReadSize 是单次 Read 的文件大小上限（字节）；<= 0 时用
	// DefaultMaxReadSize。
	MaxReadSize int64
}

// NewFileService 实现 fs.Config：按配置初始化出一个 fs.FileService。
func (c Config) NewFileService(ctx context.Context) (fs.FileService, error) {
	opts := make([]Option, 0, 2)
	if c.MaxReadSize > 0 {
		opts = append(opts, WithMaxReadSize(c.MaxReadSize))
	}
	if c.Watch {
		opts = append(opts, WithWatch(c.WatchDebounce))
	}
	f, err := New(c.Path, opts...)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// 编译期断言：Config 实现 fs.Config。
var _ fs.Config = Config{}
