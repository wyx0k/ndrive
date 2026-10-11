package remotemongo

import (
	"context"
	"encoding/json"
	"errors"

	"ntd/pkg/backend"
	"ntd/pkg/subkind"
	"ntd/pkg/tree"
)

const Name = "remote_mongo"

type Config struct {
	URI string
}

func (Config) IsBackendConfig() {}

type remotemongoBackend struct{}

func New() backend.Backend { return &remotemongoBackend{} }

func (b *remotemongoBackend) Name() string { return Name }

func (b *remotemongoBackend) SubKinds() []subkind.Kind {
	return []subkind.Kind{
		{Name: "doc", Description: "MongoDB 文档节点", Ops: []string{"doc-insert", "doc-update", "doc-delete", "doc-find"}},
	}
}

func (b *remotemongoBackend) OpsForSubKind(kind string) []tree.NamedOp {
	if kind != "doc" {
		return nil
	}
	return []tree.NamedOp{
		{Name: "doc-insert", Description: "插入文档", Schema: map[string]any{"type": "object"}, Op: stubOp("doc-insert")},
		{Name: "doc-update", Description: "更新文档", Schema: map[string]any{"type": "object"}, Op: stubOp("doc-update")},
		{Name: "doc-delete", Description: "删除文档", Schema: map[string]any{"type": "object"}, Op: stubOp("doc-delete")},
		{Name: "doc-find", Description: "查询文档", Schema: map[string]any{"type": "object"}, Op: stubOp("doc-find")},
	}
}

func (b *remotemongoBackend) ConfigFromJSON(data []byte) (backend.Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (b *remotemongoBackend) NewTree(cfg backend.Config) (tree.Tree, error) {
	if _, ok := cfg.(Config); !ok {
		return nil, errors.New("remotemongo: invalid config type")
	}
	return &stubTree{}, nil
}

type stubTree struct{}

func (t *stubTree) Get(ctx context.Context, path string) (*tree.Node, error)                  { return nil, nil }
func (t *stubTree) List(ctx context.Context, prefix string) ([]*tree.Node, error)             { return nil, nil }
func (t *stubTree) ListWithBrief(ctx context.Context, prefix string) ([]*tree.Brief, error)   { return nil, nil }
func (t *stubTree) Search(ctx context.Context, pattern string) ([]*tree.Brief, error)         { return nil, nil }
func (t *stubTree) Delete(ctx context.Context, path string) error                             { return nil }

func stubOp(name string) tree.Op {
	return func(ctx context.Context, t tree.Tree, args tree.OpArgs) (any, error) {
		return nil, errors.New(name + ": not implemented")
	}
}
