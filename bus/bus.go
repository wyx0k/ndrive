// Package bus 提供 ndrive 进程内事件传递的单点。
//
// v5 设计要点：
//   - Request 走方案 B：不开 goroutine；handler 必须自己 listen ctx 监听取消
//   - Handle 重复注册返 ErrHandlerExists（不 panic 也不覆盖）
//   - New() 无参数（v4 New(int) 的 buffer 参数无效，已 hard break）
package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// 公共错误。
var (
	ErrNoSubscriber   = errors.New("bus: no subscriber for topic")
	ErrHandlerExists  = errors.New("bus: handler already registered for topic")
	ErrRequestTimeout = errors.New("bus: request timeout")
)

// Bus 是事件传递接口。
type Bus interface {
	// Publish 把 payload 投到 topic 的所有 subscriber。
	// 不阻塞：handler 在调用方 goroutine 中按订阅顺序同步执行。
	Publish(topic string, payload any)

	// Subscribe 注册一个 subscriber，返回 subID 和 unsubscribe。
	// 同一 topic 可有多个 subscriber。
	Subscribe(topic string, handler func(any)) (subID uint64, unsubscribe func())

	// Handle 注册一个请求处理器（req/resp 模式）。
	// 同一 topic 只能有一个 handler；重复注册返 ErrHandlerExists。
	Handle(topic string, handler func(ctx context.Context, req any) (any, error)) (unsubscribe func(), err error)

	// Request 同步请求一个 topic 的 handler：
	//   1. 查 handler；没有返 ErrNoSubscriber
	//   2. 调用 h(ctx, req)
	//   3. 调用结束后检查 ctx.Err()：如果非 nil（handler 已 listen ctx 并取消），
	//      把 ctx 的 err 包装进 ErrRequestTimeout 返回
	//
	// 行为约定：handler 必须自己 listen ctx.Done() 响应取消；
	// Request 自身不开 goroutine 也不包装 ctx——handler 是 ctx 传播的终点。
	Request(ctx context.Context, topic string, req any) (any, error)
}

// defaultBus 是 Bus 的默认内存实现，进程内单例。
// 内部用 map 存 handler（O(1) 反查），用 subOrder 切片保订阅顺序（Publish 按序派发）。
type defaultBus struct {
	mu       sync.RWMutex
	subs     map[string]map[uint64]func(any)
	subOrder map[string][]uint64 // 订阅顺序切片（按 Subscribe 调用顺序）
	handlers map[string]func(ctx context.Context, req any) (any, error)
	counter  atomic.Uint64
}

// New 构造一个进程内 Bus。
//
// v5 修订：v4 的 New(int) 那个 buffer 参数无效，hard break 去掉。
func New() Bus {
	return &defaultBus{
		subs:     make(map[string]map[uint64]func(any)),
		subOrder: make(map[string][]uint64),
		handlers: make(map[string]func(ctx context.Context, req any) (any, error)),
	}
}

// Subscribe 注册 subscriber。
// 返回的 subID 是单调递增的全局 ID；unsubscribe 按 ID 精准反查。
func (b *defaultBus) Subscribe(topic string, handler func(any)) (uint64, func()) {
	id := b.counter.Add(1)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]func(any))
	}
	b.subs[topic][id] = handler
	b.subOrder[topic] = append(b.subOrder[topic], id)
	b.mu.Unlock()
	return id, func() {
		b.mu.Lock()
		delete(b.subs[topic], id)
		// 从顺序切片里移除该 id（O(n)；unsubscribe 频次低，可接受）
		order := b.subOrder[topic]
		for i, sid := range order {
			if sid == id {
				b.subOrder[topic] = append(order[:i], order[i+1:]...)
				break
			}
		}
		if len(b.subs[topic]) == 0 {
			delete(b.subs, topic)
			delete(b.subOrder, topic)
		}
		b.mu.Unlock()
	}
}

// Handle 注册 req/resp handler。
// 重复注册返 ErrHandlerExists，不 panic 也不覆盖。
func (b *defaultBus) Handle(topic string, handler func(ctx context.Context, req any) (any, error)) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.handlers[topic]; exists {
		return nil, fmt.Errorf("%w: %s", ErrHandlerExists, topic)
	}
	b.handlers[topic] = handler
	return func() {
		b.mu.Lock()
		delete(b.handlers, topic)
		b.mu.Unlock()
	}, nil
}

// Publish 同步投到所有 subscriber（按订阅顺序）。
// 先复制 (id, handler) 切片，再按顺序调用；不持锁调 handler。
func (b *defaultBus) Publish(topic string, payload any) {
	b.mu.RLock()
	order := b.subOrder[topic]
	handlers := make([]func(any), 0, len(order))
	for _, id := range order {
		handlers = append(handlers, b.subs[topic][id])
	}
	b.mu.RUnlock()
	for _, h := range handlers {
		h(payload)
	}
}

// Request 同步调用 topic 的 handler。
// v5 行为：
//   - 无 handler 返 ErrNoSubscriber
//   - 调用 h(ctx, req)
//   - 如果 ctx 在调用期间被 cancel（handler 已 listen ctx 并响应），包装 ErrRequestTimeout
//   - 否则透传 h 的 (v, err)
func (b *defaultBus) Request(ctx context.Context, topic string, req any) (any, error) {
	b.mu.RLock()
	h := b.handlers[topic]
	b.mu.RUnlock()
	if h == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoSubscriber, topic)
	}
	v, err := h(ctx, req)
	// handler 完成后检查 ctx：handler 通常会在 ctx 取消时主动 return，
	// 此时 ctx.Err() 非 nil，我们把错误包装为 ErrRequestTimeout。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return v, fmt.Errorf("%w: %v", ErrRequestTimeout, ctxErr)
	}
	return v, err
}
