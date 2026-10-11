package backend

import (
	"errors"

	"ntd/pkg/subkind"
	"ntd/pkg/tree"
)

var ErrBackendConflict = errors.New("backend name conflict")

type Config interface{ IsBackendConfig() }

type Backend interface {
	Name() string
	SubKinds() []subkind.Kind
	OpsForSubKind(kind string) []tree.NamedOp
	NewTree(cfg Config) (tree.Tree, error)
	ConfigFromJSON(data []byte) (Config, error)
}

var catalog = map[string]Backend{}

func Register(b Backend) error {
	if _, ok := catalog[b.Name()]; ok {
		return ErrBackendConflict
	}
	catalog[b.Name()] = b
	return nil
}

func Get(name string) (Backend, bool) {
	b, ok := catalog[name]
	return b, ok
}

func All() []Backend {
	out := make([]Backend, 0, len(catalog))
	for _, b := range catalog {
		out = append(out, b)
	}
	return out
}
