// Package ndrive 是 v5 容器层根包。
//
// 提供：
//   - Workspace 工作区句柄
//   - OpenWorkspace 创建/打开工作区
//   - WorkspaceConfig / MetaConfig / SqliteConfig / MysqlConfig
//
// v5 哲学：根包不是薄壳，是真实实现容器层。3 个子能力（bus / permission / tools）独立顶层包。
package ndrive

// WorkspaceConfig 是 OpenWorkspace 的配置。
type WorkspaceConfig struct {
	// WorkerID snowflake worker id。
	//   0 = 自动分配（进程内唯一，但不保证跨进程唯一）
	//   非 0 = 严格检测；同 workerID 二次 Init 会失败
	WorkerID int64

	// Meta meta 存储配置（SqliteConfig / MysqlConfig 二选一）
	Meta MetaConfig
}

// MetaConfig 是 meta 存储配置的抽象类型（SqliteConfig / MysqlConfig 二选一）。
// 用 sealed interface 模式：只有本包内的类型能实现。
type MetaConfig interface {
	isMetaConfig()
}

// SqliteConfig 本地 SQLite 存储。
type SqliteConfig struct {
	Path        string
	MaxOpenConn int
}

func (SqliteConfig) isMetaConfig() {}

// MysqlConfig 远端 MySQL 存储。
type MysqlConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	MaxOpenConns int
	MaxIdleConns int
}

func (MysqlConfig) isMetaConfig() {}
