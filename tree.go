package ntd

import "context"

// Node 树节点（全数据）
type Node struct {
	ID       int64
	ParentID int64
	Path     string
	SubKind  string
	BizModel string
	Meta     map[string]any
	Data     []byte
}

// Brief LLM 提示摘要
type Brief struct {
	ID       int64
	Path     string
	SubKind  string
	BizModel string
	Title    string
	Preview  string
}

// Tree 树接口，所有方法受 policy 控制
type Tree interface {
	Get(ctx context.Context, path string) (*Node, error)
	List(ctx context.Context, prefix string) ([]*Node, error)
	ListWithBrief(ctx context.Context, prefix string) ([]*Brief, error)
	Search(ctx context.Context, pattern string) ([]*Brief, error)
	Delete(ctx context.Context, path string) error
}
