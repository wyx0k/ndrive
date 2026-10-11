package localbadger

import (
	"context"
	"encoding/json"
	"errors"

	"ntd/pkg/backend"
	"ntd/pkg/subkind"
	"ntd/pkg/tree"
)

const Name = "local_badger"

type Config struct {
	Dir string
}

func (Config) IsBackendConfig() {}

type localbadgerBackend struct{}

func New() backend.Backend { return &localbadgerBackend{} }

func (b *localbadgerBackend) Name() string { return Name }

func (b *localbadgerBackend) SubKinds() []subkind.Kind {
	return []subkind.Kind{
		{Name: "kv", Description: "Badger KV 节点", Ops: []string{"kv-put", "kv-get", "kv-delete"}},
	}
}

func (b *localbadgerBackend) OpsForSubKind(kind string) []tree.NamedOp {
	if kind != "kv" {
		return nil
	}
	return []tree.NamedOp{
		{Name: "kv-put", Description: "写入 KV", Schema: map[string]any{"type": "object"}, Op: stubOp("kv-put")},
		{Name: "kv-get", Description: "读取 KV", Schema: map[string]any{"type": "object"}, Op: stubOp("kv-get")},
		{Name: "kv-delete", Description: "删除 KV", Schema: map[string]any{"type": "object"}, Op: stubOp("kv-delete")},
	}
}

func (b *localbadgerBackend) ConfigFromJSON(data []byte) (backend.Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (b *localbadgerBackend) NewTree(cfg backend.Config) (tree.Tree, error) {
	if _, ok := cfg.(Config); !ok {
		return nil, errors.New("localbadger: invalid config type")
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
