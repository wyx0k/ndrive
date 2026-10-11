package ntd

import (
	"context"

	pkgbus "github.com/wyx0k/ndrive/pkg/bus"
)

// Bus 是对外暴露的事件总线接口
type Bus interface {
	// Publish 单向广播，fire-and-forget
	Publish(topic string, payload any)
	// Subscribe 订阅单向事件，返回 subID 和取消函数
	Subscribe(topic string, handler func(any)) (subID uint64, unsubscribe func())
	// Handle 注册请求处理器，用于 Request/Reply 模式；同一 topic 只能有一个 handler
	Handle(topic string, handler func(ctx context.Context, req any) (any, error)) (unsubscribe func())
	// Request 发出同步请求，等待 Handle 注册的 handler 回复
	Request(ctx context.Context, topic string, req any) (any, error)
}

// busAdapter 把 pkg/bus.Bus 适配为 ntd.Bus
type busAdapter struct{ b pkgbus.Bus }

func (a *busAdapter) Publish(topic string, payload any) { a.b.Publish(topic, payload) }
func (a *busAdapter) Subscribe(topic string, handler func(any)) (uint64, func()) {
	return a.b.Subscribe(topic, handler)
}
func (a *busAdapter) Handle(topic string, handler func(ctx context.Context, req any) (any, error)) func() {
	return a.b.Handle(topic, handler)
}
func (a *busAdapter) Request(ctx context.Context, topic string, req any) (any, error) {
	return a.b.Request(ctx, topic, req)
}
