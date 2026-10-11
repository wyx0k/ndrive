package ntd

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/wyx0k/ndrive/pkg/backend"
	"github.com/wyx0k/ndrive/pkg/backend/localbadger"
	"github.com/wyx0k/ndrive/pkg/backend/localfile"
	"github.com/wyx0k/ndrive/pkg/backend/localsqlite"
	"github.com/wyx0k/ndrive/pkg/backend/remotemongo"
	"github.com/wyx0k/ndrive/pkg/backend/remotesql"
	pkgbus "github.com/wyx0k/ndrive/pkg/bus"
	pkgid "github.com/wyx0k/ndrive/pkg/id"
	pkgmeta "github.com/wyx0k/ndrive/pkg/meta"
	"github.com/wyx0k/ndrive/pkg/meta/mysql"
	"github.com/wyx0k/ndrive/pkg/meta/sqlite"
)

// Config 总配置
type Config struct {
	WorkerID  int64
	Meta      MetaConfig
	BusBuffer int
}

var (
	wsMu  sync.Mutex
	wsMap = map[string]*Workspace{}
)

func init() {
	for _, b := range []backend.Backend{
		localfile.New(),
		localsqlite.New(),
		localbadger.New(),
		remotesql.New(),
		remotemongo.New(),
	} {
		_ = backend.Register(b)
	}
}

// OpenWorkspace 创建或打开一个 workspace，同 wsID 多次调用返同一句柄。
func OpenWorkspace(ctx context.Context, wsID, ownerID string, cfg Config) (*Workspace, error) {
	workerID := int64(1)
	if cfg.WorkerID > 0 {
		workerID = cfg.WorkerID
	}
	if err := pkgid.Init(workerID); err != nil {
		return nil, err
	}

	store, err := newMetaStore(ctx, cfg.Meta)
	if err != nil {
		return nil, err
	}

	bufSize := 64
	if cfg.BusBuffer > 0 {
		bufSize = cfg.BusBuffer
	}
	b := pkgbus.New(bufSize)

	wsMu.Lock()
	defer wsMu.Unlock()
	if ws, ok := wsMap[wsID]; ok {
		// 命中缓存，关闭本次新建的 store 避免连接泄漏
		_ = store.Close()
		return ws, nil
	}
	ws, err := openWorkspace(ctx, wsID, ownerID, store, b)
	if err != nil {
		return nil, err
	}
	wsMap[wsID] = ws
	return ws, nil
}

func openWorkspace(ctx context.Context, wsID, ownerID string, store pkgmeta.Store, b pkgbus.Bus) (*Workspace, error) {
	key := wsKindPrefix + wsID
	rec, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if rec.Value == nil {
		wm := wsMeta{ID: wsID, OwnerID: ownerID}
		data, err := json.Marshal(wm)
		if err != nil {
			return nil, err
		}
		if err := store.Put(ctx, key, data); err != nil {
			return nil, err
		}
		return &Workspace{id: wsID, ownerID: ownerID, store: store, bus: b}, nil
	}
	var wm wsMeta
	if err := json.Unmarshal(rec.Value, &wm); err != nil {
		return nil, err
	}
	if wm.OwnerID != ownerID {
		return nil, fmt.Errorf("%w: ws=%s want=%s got=%s", ErrOwnerMismatch, wsID, wm.OwnerID, ownerID)
	}
	return &Workspace{id: wsID, ownerID: ownerID, store: store, bus: b}, nil
}

func newMetaStore(ctx context.Context, cfg MetaConfig) (pkgmeta.Store, error) {
	switch c := cfg.(type) {
	case SqliteConfig:
		return sqlite.New(ctx, sqlite.Config{Path: c.Path, MaxOpenConn: c.MaxOpenConn})
	case MysqlConfig:
		return mysql.New(ctx, mysql.Config{
			Host: c.Host, Port: c.Port, User: c.User, Password: c.Password,
			Database: c.Database, MaxOpenConns: c.MaxOpenConns, MaxIdleConns: c.MaxIdleConns,
		})
	default:
		return nil, fmt.Errorf("unsupported meta config type: %T", cfg)
	}
}

// resolveBackendConfig 把根包 BackendConfig 转换为各 backend 包的 Config
func resolveBackendConfig(cfg BackendConfig) (backend.Config, error) {
	switch c := cfg.(type) {
	case LocalFileConfig:
		return localfile.Config{RootDir: c.RootDir}, nil
	case LocalSQLiteConfig:
		return localsqlite.Config{DSN: c.DSN}, nil
	case LocalBadgerConfig:
		return localbadger.Config{Dir: c.Dir}, nil
	case RemoteSQLConfig:
		return remotesql.Config{DSN: c.DSN}, nil
	case RemoteMongoConfig:
		return remotemongo.Config{URI: c.URI}, nil
	default:
		return nil, fmt.Errorf("unsupported backend config type: %T", cfg)
	}
}
