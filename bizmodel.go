package ntd

import (
	"context"
	"fmt"

	"ntd/pkg/backend"
	pkgbus "ntd/pkg/bus"
	pkgctx "ntd/pkg/ctx"
	pkgmeta "ntd/pkg/meta"
	"ntd/pkg/policy"
	pkgtree "ntd/pkg/tree"
)

// BizModelType backend 能力描述
type BizModelType struct {
	Name        string
	Description string
	SubKinds    []SubKind
	Ops         []NamedOp
}

// SubKind 存储级类型
type SubKind struct {
	Name        string
	Description string
	Ops         []string
}

// Op 后端操作函数类型
type Op func(ctx context.Context, t Tree, args OpArgs) (any, error)

// OpArgs 操作参数
type OpArgs struct {
	Path string
	Args any
}

// NamedOp 命名操作
type NamedOp struct {
	Name        string
	Description string
	Schema      map[string]any
}

// BizmodelOpts 创建 bizmodel 的参数
type BizmodelOpts struct {
	ID       string
	TypeName string
	Config   BackendConfig
}

// Bizmodel 业务模型句柄
type Bizmodel struct {
	id      string
	btype   *BizModelType
	t       pkgtree.Tree
	checker policy.Checker
	bus     pkgbus.Bus
}

func newBizmodel(id string, be backend.Backend, t pkgtree.Tree, store pkgmeta.Store, wsID, ownerID string, bus pkgbus.Bus) *Bizmodel {
	btype := &BizModelType{Name: be.Name()}
	for _, sk := range be.SubKinds() {
		btype.SubKinds = append(btype.SubKinds, SubKind{Name: sk.Name, Description: sk.Description, Ops: sk.Ops})
		for _, op := range be.OpsForSubKind(sk.Name) {
			btype.Ops = append(btype.Ops, NamedOp{Name: op.Name, Description: op.Description, Schema: op.Schema})
		}
	}
	checker := policy.NewChecker(store, wsID, ownerID, bus)
	if w, ok := t.(pkgtree.Watchable); ok {
		_ = w.Watch(bus)
	}
	return &Bizmodel{id: id, btype: btype, t: t, checker: checker, bus: bus}
}


func (b *Bizmodel) ID() string          { return b.id }
func (b *Bizmodel) Type() *BizModelType { return b.btype }
func (b *Bizmodel) Ops() []NamedOp      { return b.btype.Ops }

// Tree 返回包装后的 Tree（对外使用 ntd.Tree 接口）
func (b *Bizmodel) Tree() Tree { return &treeWrapper{b.t} }

// Call 执行 op
func (b *Bizmodel) Call(ctx context.Context, opName string, path string, args any) (any, error) {
	if err := b.checker.Check(ctx, opName); err != nil {
		return nil, err
	}
	node, err := b.t.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	subKind := ""
	if node != nil {
		subKind = node.SubKind
	} else {
		subKind = inferSubKind(opName, b.btype)
		if subKind == "" {
			return nil, fmt.Errorf("op not found: cannot determine subkind for op=%s", opName)
		}
	}
	be, ok := backend.Get(b.btype.Name)
	if !ok {
		return nil, fmt.Errorf("backend not found: %s", b.btype.Name)
	}
	var namedOp pkgtree.NamedOp
	for _, op := range be.OpsForSubKind(subKind) {
		if op.Name == opName {
			namedOp = op
			break
		}
	}
	if namedOp.Op == nil {
		return nil, fmt.Errorf("op not found: %s for subkind %s", opName, subKind)
	}
	result, err := namedOp.Op(ctx, b.t, pkgtree.OpArgs{Path: path, Args: args})
	if err != nil {
		return nil, err
	}
	if namedOp.UpdateOp != "" {
		b.bus.Publish("tree.update", pkgtree.UpdateEvent{Path: path, Op: namedOp.UpdateOp})
	}
	return result, nil
}

func inferSubKind(opName string, btype *BizModelType) string {
	for _, sk := range btype.SubKinds {
		if len(opName) > len(sk.Name) && opName[:len(sk.Name)] == sk.Name && opName[len(sk.Name)] == '-' {
			return sk.Name
		}
	}
	return ""
}

// treeWrapper 把 pkg/tree.Tree 适配为根包 ntd.Tree
type treeWrapper struct{ t pkgtree.Tree }

func (w *treeWrapper) Get(ctx context.Context, path string) (*Node, error) {
	n, err := w.t.Get(ctx, path)
	if err != nil || n == nil {
		return nil, err
	}
	return &Node{ID: n.ID, ParentID: n.ParentID, Path: n.Path, SubKind: n.SubKind, BizModel: n.BizModel, Meta: n.Meta, Data: n.Data}, nil
}

func (w *treeWrapper) List(ctx context.Context, prefix string) ([]*Node, error) {
	nodes, err := w.t.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Node, len(nodes))
	for i, n := range nodes {
		out[i] = &Node{ID: n.ID, ParentID: n.ParentID, Path: n.Path, SubKind: n.SubKind, BizModel: n.BizModel, Meta: n.Meta, Data: n.Data}
	}
	return out, nil
}

func (w *treeWrapper) ListWithBrief(ctx context.Context, prefix string) ([]*Brief, error) {
	briefs, err := w.t.ListWithBrief(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return convertBriefs(briefs), nil
}

func (w *treeWrapper) Search(ctx context.Context, pattern string) ([]*Brief, error) {
	briefs, err := w.t.Search(ctx, pattern)
	if err != nil {
		return nil, err
	}
	return convertBriefs(briefs), nil
}

func (w *treeWrapper) Delete(ctx context.Context, path string) error {
	return w.t.Delete(ctx, path)
}

func convertBriefs(in []*pkgtree.Brief) []*Brief {
	out := make([]*Brief, len(in))
	for i, b := range in {
		out[i] = &Brief{ID: b.ID, Path: b.Path, SubKind: b.SubKind, BizModel: b.BizModel, Title: b.Title, Preview: b.Preview}
	}
	return out
}

// wsContext 注入 wsID 和 ownerID 到 ctx
func wsContext(ctx context.Context, wsID, ownerID string) context.Context {
	return pkgctx.WithOwner(pkgctx.WithWorkspace(ctx, wsID), ownerID)
}
