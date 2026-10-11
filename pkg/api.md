# ntd API Reference (v4)

> 与 `ARCHITECTURE.md` 配套。本文档列出引擎对外暴露的所有函数签名。
> 标记：
> - ⭐ 裸调（结构 CRUD / 基础设施，外部负责鉴权）
> - 🔒 走 `policy.Check`（bizmodel data ops）

---

## 0. 总入口

### `pkg/ntd`

```go
type Config struct {
    WorkerID  int64       // snowflake worker_id，默认 1
    Meta      MetaConfig  // SqliteConfig / MysqlConfig 二选一
    BusBuffer int         // bus channel buffer，默认 64
}

type MetaConfig interface {
    isMetaConfig()
}

type SqliteConfig struct {
    Path        string
    MaxOpenConn int
}
func (SqliteConfig) isMetaConfig() {}

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

// OpenWorkspace 创建或打开一个 workspace。
//   - wsID 不存在：创建并用传入的 ownerID
//   - wsID 存在 + ownerID 一致：打开
//   - wsID 存在 + ownerID 不一致：返 ErrOwnerMismatch
//
// 内部维护 workspace 单例 map[wsID]*Workspace，同 wsID 多次调用返同一句柄。
func OpenWorkspace(ctx context.Context, wsID, ownerID string, cfg Config) (*workspace.Workspace, error)
```

---

## 1. 基础设施

### `pkg/id` ⭐

```go
// Init 在 main 启动时调用一次；不调则 Next 用默认 worker_id=1。
// 可多次调用，最后一次生效（用于运行时切换）。
func Init(workerID ...int64) error

// Next 生成下一个 snowflake ID（int64）。
func Next() int64
```

### `pkg/meta` ⭐

```go
type Store interface {
    Get(ctx context.Context, key string) (Record, error)
    Put(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]Record, error)
    Close() error
}

type Record struct {
    Key   string
    Value []byte
}
```

#### `pkg/meta/sqlite` ⭐

```go
func New(ctx context.Context, cfg SqliteConfig) (meta.Store, error)
```

#### `pkg/meta/mysql` ⭐

```go
func New(ctx context.Context, cfg MysqlConfig) (meta.Store, error)
```

### `pkg/bus` ⭐

```go
type Bus interface {
    // Publish 非阻塞广播消息
    Publish(topic string, payload any)

    // Subscribe 订阅 topic；handler 必须非阻塞（耗时操作请内部 go func）
    Subscribe(topic string, handler func(any)) (subID uint64, unsubscribe func())

    // Request 同步等回复；超时返 ErrRequestTimeout；无订阅者返 ErrNoSubscriber
    Request(ctx context.Context, topic string, req any) (any, error)
}

func New(bufferSize int) Bus
```

### `pkg/ctx` ⭐

```go
func WithOwner(ctx context.Context, ownerID string) context.Context
func Owner(ctx context.Context) string

func WithWorkspace(ctx context.Context, wsID string) context.Context
func Workspace(ctx context.Context) string
```

---

## 2. 类型系统

### `pkg/subkind` ⭐

```go
type Kind struct {
    Name        string
    Description string
    Schema      Schema
    Ops         []string
}

type Schema struct {
    Required []Field
    Optional []Field
}

type Field struct {
    Name string
    Type string
}
```

### `pkg/tree`

```go
// Node - 全数据节点
type Node struct {
    ID       int64
    ParentID int64
    Path     string
    SubKind  string          // 存储级，backend 设
    BizModel string          // 业务级，下游设
    Meta     map[string]any
    Data     []byte
}

// Brief - LLM 提示摘要
type Brief struct {
    ID       int64
    Path     string
    SubKind  string
    BizModel string
    Title    string
    Preview  string
}

// Tree 接口（所有方法受 policy 控制）
type Tree interface {
    Get(path string) (*Node, error)                    // 🔒
    List(prefix string) ([]*Node, error)                 // 🔒 全数据
    ListWithBrief(prefix string) ([]*Brief, error)      // 🔒 仅摘要
    Delete(path string) error                           // 🔒
}

// Op & OpArgs
type Op func(ctx context.Context, t Tree, args OpArgs) (any, error)
type OpArgs struct {
    Path string
    Args any
}

type NamedOp struct {
    Name        string         // "file-fm-write"
    Description string
    Schema      map[string]any // JSON Schema
    Op          Op
}
```

---

## 3. 策略

### `pkg/policy` ⭐（policy 自身 CRUD）+ 🔒（Checker）

```go
type Mode int
const (
    RAsk Mode = iota // 读也询问（最低权限）
    R                // 只读，写全拒
    WAsk             // 写时询问，读直通
    W                // 读写全直通（最高权限）
)

type Binding struct {
    WorkspaceID string
    OwnerID     string
    Mode        Mode
}

// 元数据 CRUD（裸调，外部鉴权）
func Set(ctx context.Context, store meta.Store, b Binding) error     // ⭐
func Get(ctx context.Context, store meta.Store, wsID, ownerID string) (Mode, error)  // ⭐
func List(ctx context.Context, store meta.Store, wsID string) ([]Binding, error)      // ⭐
func Delete(ctx context.Context, store meta.Store, wsID, ownerID string) error         // ⭐

// Checker 接口（bizmodel data ops 时使用）
type Checker interface {
    Check(ctx context.Context, op string) error  // 🔒
}
func NewChecker(store meta.Store, wsID, ownerID string, bus bus.Bus) Checker
```

---

## 4. 工作区

### `pkg/workspace`

```go
type Workspace struct { ... }

func (w *Workspace) ID() string                              // ⭐
func (w *Workspace) OwnerID() string                          // ⭐
func (w *Workspace) Bus() bus.Bus                            // ⭐
func (w *Workspace) Policy() policy.Checker                  // 🔒 给 bizmodel 用
func (w *Workspace) ListBizModels(ctx context.Context) ([]*bizmodel.Bizmodel, error)  // ⭐
func (w *Workspace) GetBizmodel(ctx context.Context, bizID string) (*bizmodel.Bizmodel, error)  // ⭐
func (w *Workspace) CreateBizmodel(ctx context.Context, opts BizmodelOpts) (*bizmodel.Bizmodel, error)  // ⭐
func (w *Workspace) DeleteBizmodel(ctx context.Context, bizID string) error  // ⭐
func (w *Workspace) Close() error                            // ⭐

type BizmodelOpts struct {
    ID              string
    TypeName        string         // "local_file" 等
    Config          backend.Config // backend-specific
    UseDefaultConfig bool
}
```

---

## 5. 业务模型

### `pkg/bizmodel`

```go
type BizModelType struct {
    Name        string              // "local_file"
    Description string
    SubKinds    []subkind.Kind
    Ops         []tree.NamedOp      // 聚合
}

// Types 返回所有已注册 backend 的 BizModelType 视图（只读）
func Types() []*BizModelType       // ⭐

type Bizmodel struct { ... }

func (b *Bizmodel) ID() string                              // ⭐
func (b *Bizmodel) Type() *BizModelType                     // ⭐
func (b *Bizmodel) Tree() tree.Tree                         // 🔒（Tree 上 op 受 policy 控制）
func (b *Bizmodel) Ops() []tree.NamedOp                     // ⭐
func (b *Bizmodel) Call(ctx context.Context, opName string, path string, args any) (any, error)  // 🔒
```

#### Call 内部流程

```
1. policy.Check(ctx, opName)
   ├─ R      → 直通
   ├─ WAll   → 直通
   └─ WAsk   → bus.Request("policy.ask", payload)
                ├─ 无订阅者 → 静默 deny（返 ErrPolicyDenied）
                └─ 有订阅者 → 等回复 → 通过/拒绝
2. 校验 path 必须存在
3. JSON Schema 校验 args（依据 op.Schema）
4. 找 op（按 "<subkind>-<op>" 格式跨 SubKind 查找）
5. op.Op(ctx, tree, OpArgs{Path, Args})
6. 返回结果
```

### `pkg/bizmodel/backend` ⭐（包外**不可见**）

```go
// 包内接口，仅引擎 init 阶段使用
type Backend interface {
    Name() string
    SubKinds() []subkind.Kind
    OpsForSubKind(kind string) []tree.NamedOp
    NewTree(cfg Config) (tree.Tree, error)
}
type Config interface{ isBackendConfig() }

func Register(b Backend)       // 包内调用，重名返 ErrBackendConflict
func Get(name string) (Backend, bool)
```

---

## 6. 后端（5 个 default）

> 每个 backend 实现 `local_*` 或 `remote_*` 前缀的导出类型，导出 `New(cfg Config) Backend` 构造函数。

### `pkg/bizmodel/backend/localfile` ⭐

```go
type Config struct {
    RootDir string
}
func New(cfg Config) backend.Backend
```

### `pkg/bizmodel/backend/localsqlite` ⭐

```go
type Config struct {
    DSN string
}
func New(cfg Config) backend.Backend
```

### `pkg/bizmodel/backend/localbadger` ⭐

```go
type Config struct {
    Dir string
}
func New(cfg Config) backend.Backend
```

### `pkg/bizmodel/backend/remotesql` ⭐

```go
type Config struct {
    DSN string
}
func New(cfg Config) backend.Backend
```

### `pkg/bizmodel/backend/remotemongo` ⭐

```go
type Config struct {
    URI string
}
func New(cfg Config) backend.Backend
```

---

## 7. 占位包

> 5 个包目录存在但仅含 README，标记待 `charm.land/fantasy` 接入：

| 包 | 角色 |
|---|---|
| `pkg/service` | 暴露 workspace 给 UI 的聚合（待 UI 定型）|
| `pkg/mem` | 记忆实现 → fantasy |
| `pkg/ui` | 消费 `service` 的薄壳 → fantasy |
| `pkg/agent` | ReAct 主循环 → fantasy |
| `pkg/llm` | LLM 适配 → fantasy |

---

## 8. 错误类型

```go
var (
    // 结构 / 元数据
    ErrWorkspaceNotFound   = errors.New("workspace not found")
    ErrWorkspaceExists     = errors.New("workspace exists")
    ErrOwnerMismatch       = errors.New("workspace owner_id mismatch")
    ErrBizmodelNotFound    = errors.New("bizmodel not found")
    ErrBizmodelExists      = errors.New("bizmodel exists")

    // 后端
    ErrOpNotFound          = errors.New("op not found")
    ErrPathNotFound        = errors.New("path not found")
    ErrBackendConflict     = errors.New("backend name conflict")
    ErrBackendInitFailed   = errors.New("backend init failed")

    // 策略
    ErrPolicyDenied        = errors.New("policy denied")

    // Bus
    ErrNoSubscriber        = errors.New("no subscriber")
    ErrRequestTimeout      = errors.New("request timeout")

    // 校验 / 冲突
    ErrArgsValidation      = errors.New("args validation failed")
    ErrConcurrentConflict  = errors.New("concurrent conflict")
)
```

---

## 9. 标记说明

| 标记 | 含义 |
|---|---|
| ⭐ | 裸调，引擎不鉴权，外部负责 |
| 🔒 | 走 `policy.Check`，仅 bizmodel data ops 触发 |