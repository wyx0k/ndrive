package remotesql

import (
	"context"
	"encoding/json"
	"errors"

	"ntd/pkg/backend"
	"ntd/pkg/subkind"
	"ntd/pkg/tree"
)

const Name = "remote_sql"

type Config struct {
	DSN string
}

func (Config) IsBackendConfig() {}

type remotesqlBackend struct{}

func New() backend.Backend { return &remotesqlBackend{} }

func (b *remotesqlBackend) Name() string { return Name }

func (b *remotesqlBackend) SubKinds() []subkind.Kind {
	return []subkind.Kind{
		{Name: "table", Description: "远端 SQL 表节点", Ops: []string{"table-insert", "table-update", "table-delete", "table-query"}},
	}
}

func (b *remotesqlBackend) OpsForSubKind(kind string) []tree.NamedOp {
	if kind != "table" {
		return nil
	}
	return []tree.NamedOp{
		{Name: "table-insert", Description: "插入", Schema: map[string]any{"type": "object"}, Op: stubOp("table-insert")},
		{Name: "table-update", Description: "更新", Schema: map[string]any{"type": "object"}, Op: stubOp("table-update")},
		{Name: "table-delete", Description: "删除", Schema: map[string]any{"type": "object"}, Op: stubOp("table-delete")},
		{Name: "table-query", Description: "查询", Schema: map[string]any{"type": "object"}, Op: stubOp("table-query")},
	}
}

func (b *remotesqlBackend) ConfigFromJSON(data []byte) (backend.Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (b *remotesqlBackend) NewTree(cfg backend.Config) (tree.Tree, error) {
	if _, ok := cfg.(Config); !ok {
		return nil, errors.New("remotesql: invalid config type")
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
