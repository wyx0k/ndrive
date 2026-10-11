package localsqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wyx0k/ndrive/pkg/backend"
	"github.com/wyx0k/ndrive/pkg/subkind"
	"github.com/wyx0k/ndrive/pkg/tree"
)

const Name = "local_sqlite"

type Config struct {
	DSN string
}

func (Config) IsBackendConfig() {}

type localsqliteBackend struct{}

func New() backend.Backend { return &localsqliteBackend{} }

func (b *localsqliteBackend) Name() string { return Name }

func (b *localsqliteBackend) SubKinds() []subkind.Kind {
	return []subkind.Kind{
		{Name: "row", Description: "SQLite 行节点", Ops: []string{"row-insert", "row-update", "row-delete", "row-query"}},
	}
}

func (b *localsqliteBackend) OpsForSubKind(kind string) []tree.NamedOp {
	if kind != "row" {
		return nil
	}
	return []tree.NamedOp{
		{Name: "row-insert", Description: "插入行", Schema: map[string]any{"type": "object"}, Op: stubOp("row-insert")},
		{Name: "row-update", Description: "更新行", Schema: map[string]any{"type": "object"}, Op: stubOp("row-update")},
		{Name: "row-delete", Description: "删除行", Schema: map[string]any{"type": "object"}, Op: stubOp("row-delete")},
		{Name: "row-query", Description: "查询行", Schema: map[string]any{"type": "object"}, Op: stubOp("row-query")},
	}
}

func (b *localsqliteBackend) ConfigFromJSON(data []byte) (backend.Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (b *localsqliteBackend) NewTree(cfg backend.Config) (tree.Tree, error) {
	if _, ok := cfg.(Config); !ok {
		return nil, errors.New("localsqlite: invalid config type")
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
