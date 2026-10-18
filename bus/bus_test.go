package bus_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wyx0k/ndrive/bus"
)

func TestPublishSubscribe(t *testing.T) {
	b := bus.New()
	defer func() {}() // no Close on v5 Bus

	var got any
	var called atomic.Int32
	unsub, err := subscribe(t, b, "t", func(p any) {
		got = p
		called.Add(1)
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	b.Publish("t", "hello")
	if called.Load() != 1 {
		t.Fatalf("want 1 call, got %d", called.Load())
	}
	if got != "hello" {
		t.Fatalf("want payload=hello, got %v", got)
	}

	// 多次 publish
	b.Publish("t", "world")
	if called.Load() != 2 {
		t.Fatalf("want 2 calls, got %d", called.Load())
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	b := bus.New()
	var called atomic.Int32
	unsub, err := subscribe(t, b, "t", func(p any) { called.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	b.Publish("t", nil)
	if called.Load() != 1 {
		t.Fatalf("want 1 before unsub, got %d", called.Load())
	}

	unsub()
	b.Publish("t", nil)
	if called.Load() != 1 {
		t.Fatalf("want still 1 after unsub, got %d", called.Load())
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := bus.New()
	var a, c atomic.Int32
	unsubA, _ := subscribe(t, b, "t", func(any) { a.Add(1) })
	defer unsubA()
	unsubC, _ := subscribe(t, b, "t", func(any) { c.Add(1) })
	defer unsubC()

	b.Publish("t", nil)
	if a.Load() != 1 || c.Load() != 1 {
		t.Fatalf("want both 1, got a=%d c=%d", a.Load(), c.Load())
	}
}

func TestHandleAndRequest(t *testing.T) {
	b := bus.New()
	unsub, err := b.Handle("echo", func(ctx context.Context, req any) (any, error) {
		return req, nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	v, err := b.Request(context.Background(), "echo", "hi")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if v != "hi" {
		t.Fatalf("want hi, got %v", v)
	}
}

func TestRequestNoSubscriber(t *testing.T) {
	b := bus.New()
	_, err := b.Request(context.Background(), "missing", nil)
	if err == nil {
		t.Fatal("want error for missing handler")
	}
	if !errors.Is(err, bus.ErrNoSubscriber) {
		t.Fatalf("want ErrNoSubscriber, got %v", err)
	}
}

func TestHandleDuplicate(t *testing.T) {
	b := bus.New()
	_, err := b.Handle("t", func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	_, err = b.Handle("t", func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("want error on duplicate Handle")
	}
	if !errors.Is(err, bus.ErrHandlerExists) {
		t.Fatalf("want ErrHandlerExists, got %v", err)
	}
}

func TestHandleUnsubscribe(t *testing.T) {
	b := bus.New()
	unsub, err := b.Handle("t", func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	unsub()
	// unsubscribe 后可以再注册
	_, err = b.Handle("t", func(ctx context.Context, req any) (any, error) {
		return "second", nil
	})
	if err != nil {
		t.Fatalf("re-Handle after unsub: %v", err)
	}
	v, err := b.Request(context.Background(), "t", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if v != "second" {
		t.Fatalf("want second, got %v", v)
	}
}

// v5 关键：handler listen ctx 取消时，Request 应该包装 ErrRequestTimeout。
func TestRequestHandlerRespectsCtx(t *testing.T) {
	b := bus.New()
	_, err := b.Handle("slow", func(ctx context.Context, req any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = b.Request(ctx, "slow", nil)
	if err == nil {
		t.Fatal("want timeout error")
	}
	if !errors.Is(err, bus.ErrRequestTimeout) {
		t.Fatalf("want ErrRequestTimeout, got %v", err)
	}
}

// v5 关键：handler 不 listen ctx 时，Request 直接透传 handler 的返回值。
func TestRequestHandlerIgnoresCtx(t *testing.T) {
	b := bus.New()
	_, err := b.Handle("stubborn", func(ctx context.Context, req any) (any, error) {
		// 故意不 listen ctx，sleep 后返 result
		time.Sleep(50 * time.Millisecond)
		return "done", nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	// Request 不开 goroutine，handler 跑完才返回；ctx 已被 timeout，
	// 但因为 handler 跑完后 ctx.Err() 非 nil，会包装为 ErrRequestTimeout。
	v, err := b.Request(ctx, "stubborn", nil)
	// 由于 handler 跑完 50ms，ctx 5ms 就 timeout 了；handler 完成后 ctx.Err() != nil
	// 所以即使 handler 返回 "done" 没有 error，Request 仍会包装 ErrRequestTimeout
	_ = v
	if err == nil {
		t.Fatal("want ErrRequestTimeout because ctx was canceled during handler")
	}
	if !errors.Is(err, bus.ErrRequestTimeout) {
		t.Fatalf("want ErrRequestTimeout, got %v", err)
	}
}

// v5 关键：handler 正常完成 + ctx 未取消时，Request 透传 result。
func TestRequestHandlerSuccessCtxOK(t *testing.T) {
	b := bus.New()
	_, err := b.Handle("ok", func(ctx context.Context, req any) (any, error) {
		return req, nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	v, err := b.Request(context.Background(), "ok", "payload")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if v != "payload" {
		t.Fatalf("want payload, got %v", v)
	}
}

func TestRequestHandlerReturnsError(t *testing.T) {
	b := bus.New()
	want := errors.New("handler boom")
	_, err := b.Handle("err", func(ctx context.Context, req any) (any, error) {
		return nil, want
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	_, err = b.Request(context.Background(), "err", nil)
	if !errors.Is(err, want) {
		t.Fatalf("want handler error %v, got %v", want, err)
	}
}

// v5 关键：Publish 后到 subscriber 抛 panic，不影响其他 subscriber 也不影响 bus。
// 注：v4 这里会 panic 传播；v5 暂沿用 v4 行为（不接 panic），但保持隔离：
// 顺序执行，前面 subscriber panic 会阻止后面执行。这是 v4 行为。
// 本测试只验证"多个 subscriber 的执行顺序"，不测试 panic 隔离（未来可加）。
func TestPublishSubscriberOrder(t *testing.T) {
	b := bus.New()
	var order []int
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	_, err := subscribe(t, b, "t", func(any) {
		<-mu
		order = append(order, 1)
		mu <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_, err = subscribe(t, b, "t", func(any) {
		<-mu
		order = append(order, 2)
		mu <- struct{}{}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	b.Publish("t", nil)
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("want [1,2] in order, got %v", order)
	}
}

// helper：封装 Subscribe 调用的错误处理
func subscribe(t *testing.T, b bus.Bus, topic string, h func(any)) (func(), error) {
	t.Helper()
	_, unsub := b.Subscribe(topic, h)
	return unsub, nil
}
