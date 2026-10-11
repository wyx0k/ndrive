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
	// Handle 注册请求处理器，用于 Request/Reply 模式；同一 topic 只能有一个 handler
	Handle(topic string, handler func(ctx context.Context, req any) (any, error)) (unsubscribe func())
	Request(ctx context.Context, topic string, req any) (any, error)
}

type bus struct {
	mu       sync.RWMutex
	subs     map[string]map[uint64]func(any)
	handlers map[string]func(ctx context.Context, req any) (any, error)
	counter  atomic.Uint64
}

func New(_ int) Bus {
	return &bus{
		subs:     make(map[string]map[uint64]func(any)),
		handlers: make(map[string]func(ctx context.Context, req any) (any, error)),
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

var ErrHandlerExists = errors.New("handler already registered for topic")

func (b *bus) Handle(topic string, handler func(ctx context.Context, req any) (any, error)) func() {
	b.mu.Lock()
	if _, exists := b.handlers[topic]; exists {
		b.mu.Unlock()
		panic("ntd/bus: " + ErrHandlerExists.Error() + ": " + topic)
	}
	b.handlers[topic] = handler
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.handlers, topic)
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
	h := b.handlers[topic]
	b.mu.RUnlock()
	if h == nil {
		return nil, ErrNoSubscriber
	}
	return h(ctx, req)
}
