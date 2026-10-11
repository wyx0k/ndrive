package meta

import "context"

type Record struct {
	Key   string
	Value []byte
}

type Store interface {
	Get(ctx context.Context, key string) (Record, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Record, error)
	Close() error
}
