// Package ndrive 是 ndrive 的装配层根包。
//
// 提供：
//   - Workspace 工作区句柄
//   - OpenWorkspace 创建/打开工作区
//   - WorkspaceConfig{Meta, FS}
//
// 根包只做装配，不含任何实现分发：
//   - Meta / FS 是两个外部依赖，调用方给配置对象（实现 meta.Config /
//     fs.Config），配置对象自己负责初始化出 store / file service；
//   - bus / permission / tools 由根包内部装配，调用方不用选。
//
// 换存储后端或文件系统 = 换一份配置对象，根包一行都不用改。
package ndrive

import (
	"github.com/wyx0k/ndrive/fs"
	"github.com/wyx0k/ndrive/meta"
)

// WorkspaceConfig 是 OpenWorkspace 的配置。
type WorkspaceConfig struct {
	// Meta meta 存储配置。实现 meta.Config 的类型（sqlite.Config /
	// mysql.Config / 自定义）二选一，自己负责初始化出 meta.Store。
	Meta meta.Config

	// FS 工作区文件系统配置。实现 fs.Config 的类型（local.Config /
	// 自定义）自己负责初始化出 fs.FileService。
	// 必填：工作区必然带一个文件系统，其根目录里固定包含 /.tmp 与 /.mem
	// 两个约定目录。
	FS fs.Config
}
