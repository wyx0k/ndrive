package ntd

// MetaConfig meta 存储配置（SqliteConfig / MysqlConfig 二选一）
type MetaConfig interface{ isMetaConfig() }

// SqliteConfig 本地 SQLite
type SqliteConfig struct {
	Path        string
	MaxOpenConn int
}

func (SqliteConfig) isMetaConfig() {}

// MysqlConfig 远端 MySQL
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

// BackendConfig backend 存储配置基类型
type BackendConfig interface{ IsBackendConfig() }

// --- Backend 名称常量 ---

const (
	BackendLocalFile   = "local_file"
	BackendLocalSQLite = "local_sqlite"
	BackendLocalBadger = "local_badger"
	BackendRemoteSQL   = "remote_sql"
	BackendRemoteMongo = "remote_mongo"
)

// --- Backend Config 类型 ---

// LocalFileConfig localfile backend 配置
type LocalFileConfig struct {
	RootDir string
}

func (LocalFileConfig) IsBackendConfig() {}

// LocalSQLiteConfig localsqlite backend 配置
type LocalSQLiteConfig struct {
	DSN string
}

func (LocalSQLiteConfig) IsBackendConfig() {}

// LocalBadgerConfig localbadger backend 配置
type LocalBadgerConfig struct {
	Dir string
}

func (LocalBadgerConfig) IsBackendConfig() {}

// RemoteSQLConfig remotesql backend 配置
type RemoteSQLConfig struct {
	DSN string
}

func (RemoteSQLConfig) IsBackendConfig() {}

// RemoteMongoConfig remotemongo backend 配置
type RemoteMongoConfig struct {
	URI string
}

func (RemoteMongoConfig) IsBackendConfig() {}
