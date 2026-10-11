package id

import (
	"sync"

	"github.com/bwmarrin/snowflake"
)

var (
	mu   sync.Mutex
	node *snowflake.Node
)

func Init(workerID ...int64) error {
	id := int64(1)
	if len(workerID) > 0 {
		id = workerID[0]
	}
	mu.Lock()
	defer mu.Unlock()
	n, err := snowflake.NewNode(id)
	if err != nil {
		return err
	}
	node = n
	return nil
}

func Next() int64 {
	mu.Lock()
	n := node
	mu.Unlock()
	if n == nil {
		_ = Init()
		mu.Lock()
		n = node
		mu.Unlock()
	}
	return n.Generate().Int64()
}
