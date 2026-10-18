// Package id 提供 snowflake ID 工厂。
//
// v5 行为：
//   - Init(workerID) 全局只调一次；二次 Init 返 ErrAlreadyInit
//   - workerID == 0：自动从进程派 ID（PID-based）
//   - Next() 返 snowflake ID；未 Init 时返 ErrNotInit
package id

import (
	"errors"
	"os"
	"sync"

	"github.com/bwmarrin/snowflake"
)

var (
	mu   sync.Mutex
	node *snowflake.Node
)

var (
	ErrAlreadyInit   = errors.New("id: already initialized")
	ErrNotInit       = errors.New("id: not initialized")
	ErrInvalidWorker = errors.New("id: invalid worker id")
)

// Init 初始化 snowflake 节点。
//   - workerID == 0：自动从进程派生（PID mod 1024）
//   - workerID != 0：snowflake 标准（0~1023）
//   - 二次 Init 返 ErrAlreadyInit（不论 workerID 是否相同）
func Init(workerID int64) error {
	mu.Lock()
	defer mu.Unlock()
	if node != nil {
		return ErrAlreadyInit
	}
	if workerID == 0 {
		workerID = autoWorkerID()
	}
	if workerID < 0 || workerID > 1023 {
		return ErrInvalidWorker
	}
	n, err := snowflake.NewNode(workerID)
	if err != nil {
		return err
	}
	node = n
	return nil
}

// Next 返下一个 snowflake ID。
func Next() (int64, error) {
	mu.Lock()
	n := node
	mu.Unlock()
	if n == nil {
		return 0, ErrNotInit
	}
	return n.Generate().Int64(), nil
}

func autoWorkerID() int64 {
	pid := os.Getpid()
	return int64(pid % 1024)
}
