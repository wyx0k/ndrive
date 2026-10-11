package tree

import "context"

type Node struct {
	ID       int64
	ParentID int64
	Path     string
	SubKind  string
	BizModel string
	Meta     map[string]any
	Data     []byte
}

type Brief struct {
	ID       int64
	Path     string
	SubKind  string
	BizModel string
	Title    string
	Preview  string
}

type Tree interface {
	Get(ctx context.Context, path string) (*Node, error)
	List(ctx context.Context, prefix string) ([]*Node, error)
	ListWithBrief(ctx context.Context, prefix string) ([]*Brief, error)
	Search(ctx context.Context, pattern string) ([]*Brief, error)
	Delete(ctx context.Context, path string) error
}

type Op func(ctx context.Context, t Tree, args OpArgs) (any, error)

type OpArgs struct {
	Path string
	Args any
}

type NamedOp struct {
	Name        string
	Description string
	Schema      map[string]any
	Op          Op
	// UpdateOp 非空时表示此 op 是写操作，执行成功后引擎会发布 tree.update 事件
	UpdateOp UpdateOp
}

// UpdateOp 文件变更类型
type UpdateOp string

const (
	UpdateCreate UpdateOp = "create"
	UpdateWrite  UpdateOp = "write"
	UpdateDelete UpdateOp = "delete"
	UpdateRename UpdateOp = "rename"
)

// UpdateEvent tree.update 事件 payload
type UpdateEvent struct {
	Path string
	Op   UpdateOp
}

// Publisher bus 发布接口最小子集
type Publisher interface {
	Publish(topic string, payload any)
}

// Watchable 可选接口，backend tree 实现后由引擎注入 bus 并启动监听
type Watchable interface {
	Watch(pub Publisher) error
	StopWatch()
}
