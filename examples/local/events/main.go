// events 演示 ndrive 的事件总线（bus.Bus）用法。
//
// 覆盖：
//   - Subscribe：订阅插件生命周期事件 plugin.<id>.{start,done,error}
//   - Publish：插件内部自定义事件（如 note.created）
//   - Handle + Request：req/resp 模式，用于 permission.ask 等同步问答场景
//
// 运行：go run ./examples/local/events
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-events.db"
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)

	ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: dbPath},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	// 本示例只调用读操作；设成 RAsk 让读操作直接放行。
	if err := ws.SetPolicy(ctx, "alice", permission.RAsk); err != nil {
		panic(err)
	}

	// 1. Subscribe：订阅插件调用的生命周期事件。
	// 事件 topic 格式固定为 plugin.<id>.<start|done|error>。
	var mu sync.Mutex
	var log []string
	for _, op := range []string{"start", "done", "error"} {
		topic := "plugin.func.greet." + op
		_, unsub := ws.Bus().Subscribe(topic, func(p any) {
			evt, ok := p.(tools.CallEvent)
			if !ok {
				return
			}
			mu.Lock()
			log = append(log, fmt.Sprintf("[event] topic=%s op=%s args=%v result=%v err=%s",
				topic, evt.Op, evt.Args, evt.Result, evt.Err))
			mu.Unlock()
		})
		defer unsub()
	}

	// 2. 注册一个会发自定义事件的插件。
	if err := ws.Register(tools.SimplePluginDef{
		ID_:          "func.greet",
		Description_: "打招呼并发出自定义事件 greet.sent",
		Read_:        true,
		Fn_: func(_ context.Context, ws tools.Context, args any) (any, error) {
			a, _ := args.(map[string]any)
			name, _ := a["name"].(string)
			msg := "hello, " + name
			// Publish：插件内部发自定义事件，和生命周期事件用不同 topic 空间。
			ws.Bus().Publish("greet.sent", map[string]any{"name": name})
			return msg, nil
		},
	}); err != nil {
		panic(err)
	}

	// 3. 订阅自定义事件。
	_, unsubGreet := ws.Bus().Subscribe("greet.sent", func(p any) {
		fmt.Printf("[custom event] greet.sent payload=%v\n", p)
	})
	defer unsubGreet()

	// 触发调用，驱动上面两类事件。
	v, err := ws.API().Call(ctx, "func.greet", map[string]any{"name": "alice"})
	if err != nil {
		panic(err)
	}
	fmt.Println("call result:", v)

	mu.Lock()
	for _, line := range log {
		fmt.Println(line)
	}
	mu.Unlock()

	// 4. Handle + Request：req/resp 模式。
	// Handle 注册一个同步处理器；Request 发起请求并等待返回。
	unsubHandle, err := ws.Bus().Handle("math.square", func(_ context.Context, req any) (any, error) {
		n, _ := req.(int)
		return n * n, nil
	})
	if err != nil {
		panic(err)
	}
	defer unsubHandle()

	result, err := ws.Bus().Request(ctx, "math.square", 7)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Request(math.square, 7) = %v\n", result)

	// 对没有 handler 的 topic 发 Request 会返回 ErrNoSubscriber。
	_, err = ws.Bus().Request(ctx, "nobody.listens", nil)
	fmt.Println("Request on topic with no handler:", err, "is ErrNoSubscriber:", errors.Is(err, bus.ErrNoSubscriber))
}
