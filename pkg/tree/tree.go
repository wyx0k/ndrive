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
}
