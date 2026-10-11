package ntd

import (
	"context"

	"ntd/pkg/backend"
	"ntd/pkg/subkind"
	pkgtree "ntd/pkg/tree"
)

// BackendProvider 是下游实现自定义 backend 的接口，全部使用根包类型，无需导入 pkg/* 。
type BackendProvider interface {
	Name() string
	SubKinds() []SubKind
	OpsForSubKind(kind string) []ExternalOp
	NewTree(cfg BackendConfig) (Tree, error)
	ConfigFromJSON(data []byte) (BackendConfig, error)
}

// ExternalOp 下游定义的 op，使用根包类型。
type ExternalOp struct {
	Name        string
	Description string
	Schema      map[string]any
	Op          func(ctx context.Context, t Tree, args OpArgs) (any, error)
}

// RegisterBackend 注册自定义 backend，必须在 OpenWorkspace 之前调用。
func RegisterBackend(p BackendProvider) error {
	return backend.Register(&externalBackendAdapter{p: p})
}

// externalBackendAdapter 把 BackendProvider 适配为 pkg/backend.Backend
type externalBackendAdapter struct {
	p BackendProvider
}

func (a *externalBackendAdapter) Name() string { return a.p.Name() }

func (a *externalBackendAdapter) SubKinds() []subkind.Kind {
	sks := a.p.SubKinds()
	out := make([]subkind.Kind, len(sks))
	for i, sk := range sks {
		out[i] = subkind.Kind{Name: sk.Name, Description: sk.Description, Ops: sk.Ops}
	}
	return out
}

func (a *externalBackendAdapter) OpsForSubKind(kind string) []pkgtree.NamedOp {
	ops := a.p.OpsForSubKind(kind)
	out := make([]pkgtree.NamedOp, len(ops))
	for i, op := range ops {
		out[i] = pkgtree.NamedOp{
			Name:        op.Name,
			Description: op.Description,
			Schema:      op.Schema,
			Op: func(ctx context.Context, t pkgtree.Tree, args pkgtree.OpArgs) (any, error) {
				return op.Op(ctx, &treeWrapper{t}, OpArgs{Path: args.Path, Args: args.Args})
			},
		}
	}
	return out
}

// NewTree: cfg 来自引擎内部，外部 Config 同时满足 backend.Config 和 BackendConfig，直接传递。
func (a *externalBackendAdapter) NewTree(cfg backend.Config) (pkgtree.Tree, error) {
	extCfg, ok := cfg.(BackendConfig)
	if !ok {
		extCfg = opaqueBackendConfig{cfg}
	}
	t, err := a.p.NewTree(extCfg)
	if err != nil {
		return nil, err
	}
	return &externalTreeAdapter{t: t}, nil
}

func (a *externalBackendAdapter) ConfigFromJSON(data []byte) (backend.Config, error) {
	return a.p.ConfigFromJSON(data)
}

// opaqueBackendConfig 兜底包装：当内部 Config 未实现 BackendConfig 时使用。
// 实际上外部注册的 Config 始终同时实现两个接口，此类型几乎不会被实例化。
type opaqueBackendConfig struct{ backend.Config }

func (opaqueBackendConfig) IsBackendConfig() {}

// externalTreeAdapter 把 ntd.Tree 适配为 pkg/tree.Tree
type externalTreeAdapter struct{ t Tree }

func (a *externalTreeAdapter) Get(ctx context.Context, path string) (*pkgtree.Node, error) {
	n, err := a.t.Get(ctx, path)
	if err != nil || n == nil {
		return nil, err
	}
	return &pkgtree.Node{ID: n.ID, ParentID: n.ParentID, Path: n.Path, SubKind: n.SubKind, BizModel: n.BizModel, Meta: n.Meta, Data: n.Data}, nil
}

func (a *externalTreeAdapter) List(ctx context.Context, prefix string) ([]*pkgtree.Node, error) {
	nodes, err := a.t.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*pkgtree.Node, len(nodes))
	for i, n := range nodes {
		out[i] = &pkgtree.Node{ID: n.ID, ParentID: n.ParentID, Path: n.Path, SubKind: n.SubKind, BizModel: n.BizModel, Meta: n.Meta, Data: n.Data}
	}
	return out, nil
}

func (a *externalTreeAdapter) ListWithBrief(ctx context.Context, prefix string) ([]*pkgtree.Brief, error) {
	briefs, err := a.t.ListWithBrief(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return toInternalBriefs(briefs), nil
}

func (a *externalTreeAdapter) Search(ctx context.Context, pattern string) ([]*pkgtree.Brief, error) {
	briefs, err := a.t.Search(ctx, pattern)
	if err != nil {
		return nil, err
	}
	return toInternalBriefs(briefs), nil
}

func (a *externalTreeAdapter) Delete(ctx context.Context, path string) error {
	return a.t.Delete(ctx, path)
}

func toInternalBriefs(in []*Brief) []*pkgtree.Brief {
	out := make([]*pkgtree.Brief, len(in))
	for i, b := range in {
		out[i] = &pkgtree.Brief{ID: b.ID, Path: b.Path, SubKind: b.SubKind, BizModel: b.BizModel, Title: b.Title, Preview: b.Preview}
	}
	return out
}
