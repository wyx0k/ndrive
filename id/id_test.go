package id_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/wyx0k/ndrive/id"
)

// 注意：id 包进程级单例，测试间靠 sync.Mutex 串行化。
// 首次 Init 成功；后续 Init 返 ErrAlreadyInit。
var resetMu sync.Mutex

func TestInitZeroWorker(t *testing.T) {
	resetMu.Lock()
	defer resetMu.Unlock()
	err := id.Init(0)
	if err != nil && !errors.Is(err, id.ErrAlreadyInit) {
		t.Errorf("Init(0): %v", err)
	}
}

func TestInitExplicit(t *testing.T) {
	resetMu.Lock()
	defer resetMu.Unlock()
	err := id.Init(42)
	if err != nil && !errors.Is(err, id.ErrAlreadyInit) {
		t.Errorf("Init(42): %v", err)
	}
}

func TestNextAfterInit(t *testing.T) {
	resetMu.Lock()
	defer resetMu.Unlock()
	_ = id.Init(0)
	v, err := id.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if v <= 0 {
		t.Errorf("Next should be positive, got %d", v)
	}
	v2, _ := id.Next()
	if v == v2 {
		t.Error("Next should return unique ids")
	}
}

func TestInitInvalidWorker(t *testing.T) {
	resetMu.Lock()
	defer resetMu.Unlock()
	err := id.Init(2000)
	if err == nil {
		t.Fatal("want error for workerID > 1023")
	}
	if !errors.Is(err, id.ErrInvalidWorker) && !errors.Is(err, id.ErrAlreadyInit) {
		t.Errorf("want ErrInvalidWorker or ErrAlreadyInit, got %v", err)
	}
}

var _ sync.Mutex
