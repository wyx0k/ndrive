package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrNoSubscriber   = errors.New("no subscriber")
	ErrRequestTimeout = errors.New("request timeout")
)

type Bus interface {
	Publish(topic string, payload any)
	Subscribe(topic string, handler func(any)) (subID uint64, unsubscribe func())
	Request(ctx context.Context, topic string, req any) (any, error)
}

type bus struct {
	mu      sync.RWMutex
	subs    map[string]map[uint64]func(any)
	counter atomic.Uint64
	buf     int
}

func New(bufferSize int) Bus {
	return &bus{
		subs: make(map[string]map[uint64]func(any)),
		buf:  bufferSize,
	}
}

func (b *bus) Subscribe(topic string, handler func(any)) (uint64, func()) {
	id := b.counter.Add(1)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]func(any))
	}
	b.subs[topic][id] = handler
	b.mu.Unlock()
	return id, func() {
		b.mu.Lock()
		delete(b.subs[topic], id)
		b.mu.Unlock()
	}
}

func (b *bus) Publish(topic string, payload any) {
	b.mu.RLock()
	handlers := make([]func(any), 0, len(b.subs[topic]))
	for _, h := range b.subs[topic] {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()
	for _, h := range handlers {
		h(payload)
	}
}

func (b *bus) Request(ctx context.Context, topic string, req any) (any, error) {
	b.mu.RLock()
	count := len(b.subs[topic])
	b.mu.RUnlock()
	if count == 0 {
		return nil, ErrNoSubscriber
	}

	ch := make(chan any, 1)
	id := b.counter.Add(1)

	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]func(any))
	}
	b.subs[topic][id] = func(payload any) {
		select {
		case ch <- payload:
		default:
		}
	}
	b.mu.Unlock()

	// 向除临时 handler 外的订阅者广播
	b.mu.RLock()
	handlers := make([]func(any), 0)
	for hid, h := range b.subs[topic] {
		if hid != id {
			handlers = append(handlers, h)
		}
	}
	b.mu.RUnlock()

	if len(handlers) == 0 {
		b.mu.Lock()
		delete(b.subs[topic], id)
		b.mu.Unlock()
		return nil, ErrNoSubscriber
	}

	for _, h := range handlers {
		h(req)
	}

	defer func() {
		b.mu.Lock()
		delete(b.subs[topic], id)
		b.mu.Unlock()
	}()

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ErrRequestTimeout
	}
}
