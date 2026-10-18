// Package main 是 ndrive v5 的端到端综合示例。
//
// 演示：
//  1. Workspace 生命周期（OpenWorkspace + ownership check + Close）
//  2. Policy 管理（4 档 mode：R / RAsk / W / WAsk）
//  3. Plugin 注册（SimplePluginDef + 自定义 Plugin 接口）
//  4. RegisterAll 批量注册 + 拓扑排序
//  5. Plugin 依赖（DependsOn + 跨 plugin 调用）
//  6. Tool / Func 双视图投影
//  7. Permission WAsk 询问流程（bus.Request 端到端）
//  8. Bus 事件订阅（plugin.<id>.start / done / error）
//  9. 错误处理（plugin not found / permission denied / 重复 userID）
//
// 运行：go run ./examples/comprehensive
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/meta"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
	"github.com/wyx0k/ndrive/tools/builtin"
)

func main() {
	ctx := context.Background()

	// ─────────────────────────────────────────────────────────────────
	// #1 Workspace 生命周期
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("#1 Workspace 生命周期")

	dbPath := "/tmp/ndrive-comprehensive.db"
	_ = os.Remove(dbPath) // 干净启动

	ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: dbPath},
	})
	must(err, "OpenWorkspace")
	defer ws.Close()
	fmt.Printf("  ✓ workspace=%s user=%s bus=%T api=%T\n",
		ws.ID(), ws.UserID(), ws.Bus(), ws.API())

	// ownership 保护：换 user 重开同一 wsID 应被拒
	_, err = ndrive.OpenWorkspace(ctx, "demo", "bob", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: dbPath},
	})
	if errors.Is(err, meta.ErrWorkspaceExists) {
		fmt.Println("  ✓ ownership check: bob 被拒（demo 已属 alice）")
	} else {
		must(err, "ownership check")
	}

	// ─────────────────────────────────────────────────────────────────
	// #2 Bus 事件订阅 + 插件生命周期事件
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#2 Bus 事件订阅")

	var events []tools.CallEvent
	var evMu sync.Mutex
	ws.Bus().Subscribe("plugin.>", func(p any) { // '>' 是通配；本实现走精确 topic
		evt, ok := p.(tools.CallEvent)
		if !ok {
			return
		}
		evMu.Lock()
		events = append(events, evt)
		evMu.Unlock()
	})
	// 实际订阅每个 topic
	stopAll := subscribeLifecycle(ws.Bus(), &events, &evMu)
	defer stopAll()
	fmt.Println("  ✓ 订阅 plugin.<id>.{start,done,error} 全部事件")

	// ─────────────────────────────────────────────────────────────────
	// #3 Plugin 注册：SimplePluginDef（声明式）+ 自定义 Plugin 接口
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#3 Plugin 注册")

	// 3.1 opt-in 内置 plugin
	must(ws.Register(builtin.Echo()), "register builtin.Echo")

	// 3.2 SimplePluginDef 声明式 plugin
	must(ws.Register(tools.SimplePluginDef{
		ID_:          "util.now",
		Description_: "返回当前时间（RFC3339）",
		Schema_:      map[string]any{"type": "object"},
		Read_:        true,
		Fn_: func(_ context.Context, _ tools.Context, _ any) (any, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	}), "register util.now")

	// 3.3 自定义 Plugin 接口实现（演示 Plugin 接口 6 个方法）
	must(ws.Register(newGreeterPlugin()), "register greeter.Plugin")

	// 3.4 带依赖的 plugin（func.calc 依赖 util.now）
	must(ws.Register(tools.SimplePluginDef{
		ID_:          "func.calc",
		Description_: "简单算术：op ∈ {add, sub, mul, div}",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"a", "b", "op"},
			"properties": map[string]any{
				"a":  map[string]any{"type": "number"},
				"b":  map[string]any{"type": "number"},
				"op": map[string]any{"type": "string", "enum": []string{"add", "sub", "mul", "div"}},
			},
		},
		Read_:      true, // 纯计算，标记为读
		DependsOn_: []tools.PluginID{"util.now"},
		Fn_:        calcFn,
	}), "register func.calc (depends on util.now)")

	// ─────────────────────────────────────────────────────────────────
	// #4 RegisterAll 批量注册（含跨 batch 依赖）
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#4 RegisterAll 批量注册")

	// 这一批：func.notes.{create,read,list} 互相依赖
	batch := []tools.Plugin{
		tools.SimplePluginDef{
			ID_:          "func.notes.create",
			Description_: "创建一条 note（写文件）",
			Schema_: map[string]any{
				"type":     "object",
				"required": []string{"title", "body"},
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
					"body":  map[string]any{"type": "string"},
				},
			},
			Read_: false,
			Fn_:   notesCreateFn,
		},
		tools.SimplePluginDef{
			ID_:          "func.notes.read",
			Description_: "读一条 note",
			Schema_: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
			},
			Read_:      true,
			DependsOn_: []tools.PluginID{"func.notes.create"},
			Fn_:        notesReadFn,
		},
		tools.SimplePluginDef{
			ID_:          "func.notes.list",
			Description_: "列出所有 note id",
			Schema_:      map[string]any{"type": "object"},
			Read_:        true,
			DependsOn_:   []tools.PluginID{"func.notes.create"},
			Fn_:          notesListFn,
		},
	}
	must(ws.API().RegisterAll(batch), "RegisterAll(notes)")

	// Resolve 看拓扑序
	order, err := ws.API().Resolve()
	must(err, "Resolve")
	fmt.Printf("  ✓ 拓扑序（依赖在前）：%v\n", order)

	// ─────────────────────────────────────────────────────────────────
	// #5 Tool / Func 双视图投影
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#5 Tool / Func 双视图")

	llmView, _ := json.MarshalIndent(ws.API().Tools(), "  ", "  ")
	fmt.Printf("  LLM 视角（首 200 字符）：\n  %s...\n", truncate(string(llmView), 200))

	uiFuncs := ws.API().Funcs()
	fmt.Printf("  UI 视角：%d 个 func，已 bind Call\n", len(uiFuncs))

	// ─────────────────────────────────────────────────────────────────
	// #6 Policy：4 档 mode 演示 + 切换
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#6 Policy（4 档 mode）")

	demo := func(mode permission.Mode) {
		must(ws.SetPolicy(ctx, "alice", mode), "SetPolicy")
		current, _ := ws.GetPolicy(ctx, "alice")
		fmt.Printf("  mode=%s  → ", current)
		v, err := ws.API().Call(ctx, "util.echo", map[string]any{"x": 1})
		if err != nil {
			fmt.Printf("denied: %v\n", err)
		} else {
			fmt.Printf("ok result=%v\n", v)
		}
	}
	demo(permission.R)    // 只读；echo 是读 → ok
	demo(permission.W)    // 全写；echo 是读 → denied
	demo(permission.RAsk) // 读放行，写询问；echo 是读 → ok
	demo(permission.WAsk) // 全部询问；echo 是读 → ask（无人订阅，denied）

	// 恢复 RAsk 继续后续演示
	must(ws.SetPolicy(ctx, "alice", permission.RAsk), "reset RAsk")

	// ─────────────────────────────────────────────────────────────────
	// #7 跨 plugin 调用（func.calc 内部用 util.now 演示依赖）
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#7 跨 plugin 调用")

	// calc 内部会通过 ws.API().Call(util.now, ...)，演示 DependsOn
	v, err := ws.API().Call(ctx, "func.calc", map[string]any{"a": 10, "b": 4, "op": "mul"})
	must(err, "func.calc mul")
	fmt.Printf("  ✓ 10 * 4 = %v\n", v)

	v, err = ws.API().Call(ctx, "func.calc", map[string]any{"a": 10, "b": 4, "op": "div"})
	must(err, "func.calc div")
	fmt.Printf("  ✓ 10 / 4 = %v\n", v)

	// ─────────────────────────────────────────────────────────────────
	// #8 Permission WAsk 询问流程（端到端 bus.Request/Handle）
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#8 Permission WAsk 询问流程")

	// 写操作需要询问；UI 端先订阅 ask
	unsubAsk, err := ws.Bus().Handle("permission.ask", func(_ context.Context, req any) (any, error) {
		payload, _ := req.(permission.AskPayload)
		fmt.Printf("  [UI 收到询问] op=%s ws=%s user=%s preview=%q\n",
			payload.Op, payload.WorkspaceID, payload.UserID, payload.Preview)
		// 模拟：50ms 后自动批准（同步 sleep，不走 channel 避免一次性陷阱）
		time.Sleep(50 * time.Millisecond)
		return permission.AskResponse{Approved: true}, nil
	})
	must(err, "Handle permission.ask")
	defer unsubAsk()

	// 临时把 alice 切到 WAsk
	must(ws.SetPolicy(ctx, "alice", permission.WAsk), "SetPolicy WAsk")
	defer func() { _ = ws.SetPolicy(ctx, "alice", permission.RAsk) }()

	// 触发一个写操作（func.notes.create 是 Write）
	created, err := ws.API().Call(ctx, "func.notes.create", map[string]any{
		"title": "first", "body": "hello ndrive",
	})
	must(err, "notes.create")
	fmt.Printf("  ✓ create: %v\n", created)

	// 再读回来
	read, err := ws.API().Call(ctx, "func.notes.read", map[string]any{"id": "first"})
	must(err, "notes.read")
	fmt.Printf("  ✓ read: %v\n", read)

	listed, err := ws.API().Call(ctx, "func.notes.list", nil)
	must(err, "notes.list")
	fmt.Printf("  ✓ list: %v\n", listed)

	// ─────────────────────────────────────────────────────────────────
	// #9 错误处理
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#9 错误处理")

	// 9.1 plugin not found
	_, err = ws.API().Call(ctx, "ghost.plugin", nil)
	fmt.Printf("  ✓ not found: %v\n", err)

	// 9.2 重复注册
	err = ws.Register(builtin.Echo())
	fmt.Printf("  ✓ duplicate: %v\n", err)

	// 9.3 依赖未注册
	err = ws.Register(tools.SimplePluginDef{
		ID_:        "func.broken",
		Fn_:        func(_ context.Context, _ tools.Context, _ any) (any, error) { return nil, nil },
		DependsOn_: []tools.PluginID{"nonexistent.dep"},
	})
	fmt.Printf("  ✓ missing dep: %v\n", err)

	// 9.4 跨 plugin 失败时上游拿到 error
	_, err = ws.API().Call(ctx, "func.calc", map[string]any{"a": 1, "b": 0, "op": "div"})
	fmt.Printf("  ✓ calc 1/0: %v\n", err)

	// ─────────────────────────────────────────────────────────────────
	// #10 回顾：实际发了哪些生命周期事件
	// ─────────────────────────────────────────────────────────────────
	fmt.Println("\n#10 生命周期事件统计")
	evMu.Lock()
	counts := map[string]int{}
	for _, e := range events {
		counts[e.PluginID+"."+e.Op]++
	}
	evMu.Unlock()
	for k, n := range counts {
		fmt.Printf("  %-40s × %d\n", k, n)
	}

	fmt.Println("\n✓ 全部演示完成")
}

// ─────────────────────────────────────────────────────────────────────────────
// 辅助
// ─────────────────────────────────────────────────────────────────────────────

func must(err error, label string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", label, err)
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// subscribeLifecycle 订阅 plugin.*.* 三类事件并 push 进 events。
// 返 stop 函数用于批量取消订阅。
func subscribeLifecycle(b bus.Bus, events *[]tools.CallEvent, mu *sync.Mutex) func() {
	topics := []string{
		"plugin.util.echo.start", "plugin.util.echo.done", "plugin.util.echo.error",
		"plugin.util.now.start", "plugin.util.now.done", "plugin.util.now.error",
		"plugin.greeter.greet.start", "plugin.greeter.greet.done", "plugin.greeter.greet.error",
		"plugin.func.calc.start", "plugin.func.calc.done", "plugin.func.calc.error",
		"plugin.func.notes.create.start", "plugin.func.notes.create.done", "plugin.func.notes.create.error",
		"plugin.func.notes.read.start", "plugin.func.notes.read.done", "plugin.func.notes.read.error",
		"plugin.func.notes.list.start", "plugin.func.notes.list.done", "plugin.func.notes.list.error",
	}
	stops := make([]func(), 0, len(topics))
	for _, t := range topics {
		_, unsub := b.Subscribe(t, func(p any) {
			evt, ok := p.(tools.CallEvent)
			if !ok {
				return
			}
			mu.Lock()
			*events = append(*events, evt)
			mu.Unlock()
		})
		stops = append(stops, unsub)
	}
	return func() {
		for _, s := range stops {
			s()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 自定义 Plugin 接口实现：演示 6 个方法手写
// ─────────────────────────────────────────────────────────────────────────────

type greeterPlugin struct {
	calls int
}

func newGreeterPlugin() *greeterPlugin { return &greeterPlugin{} }

func (g *greeterPlugin) ID() tools.PluginID { return "greeter.greet" }
func (g *greeterPlugin) Description() string {
	return "打招呼：name → hello, <name>!（#call 次）"
}
func (g *greeterPlugin) Schema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
}
func (g *greeterPlugin) DependsOn() []tools.PluginID { return nil }
func (g *greeterPlugin) Read() bool                  { return true }
func (g *greeterPlugin) Call(_ context.Context, _ tools.Context, args any) (any, error) {
	g.calls++
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("greeter: want map args")
	}
	name, _ := a["name"].(string)
	return fmt.Sprintf("hello, %s! (#%d)", name, g.calls), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// calc plugin 内部通过 ws.API() 调 util.now（演示跨 plugin 依赖）
// ─────────────────────────────────────────────────────────────────────────────

func calcFn(ctx context.Context, ws tools.Context, args any) (any, error) {
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("calc: want map args")
	}
	x := asFloat(a["a"])
	y := asFloat(a["b"])
	op, _ := a["op"].(string)

	// 调依赖 plugin（util.now）—— 即使算式不需要，也走一次证明依赖通路
	if _, err := ws.API().Call(ctx, "util.now", nil); err != nil {
		return nil, fmt.Errorf("calc: dep util.now failed: %w", err)
	}

	switch op {
	case "add":
		return x + y, nil
	case "sub":
		return x - y, nil
	case "mul":
		return x * y, nil
	case "div":
		if y == 0 {
			return nil, errors.New("calc: division by zero")
		}
		return x / y, nil
	}
	return nil, fmt.Errorf("calc: unknown op %q", op)
}

// asFloat 兼容 LLM 经 JSON 反序列化的 float64 和直传 int 的字面量。
func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// ─────────────────────────────────────────────────────────────────────────────
// notes 三件套（共享 state，演示 plugin 间协作）
// ─────────────────────────────────────────────────────────────────────────────

// 真实场景会用 meta.NewPermissionStore 之外的某个 store；这里为示例用内存 map + mutex。
// （plugin 之间的共享状态可以走 ws.Bus() 发事件 / 或自己持一个 store 句柄）
var (
	notesMu sync.Mutex
	notes   = map[string]string{} // id → body
)

func notesCreateFn(_ context.Context, ws tools.Context, args any) (any, error) {
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("notes.create: want map args")
	}
	title, _ := a["title"].(string)
	body, _ := a["body"].(string)

	notesMu.Lock()
	notes[title] = body
	notesMu.Unlock()

	ws.Bus().Publish("note.created", map[string]any{"id": title})
	return map[string]any{"id": title, "ok": true}, nil
}

func notesReadFn(_ context.Context, _ tools.Context, args any) (any, error) {
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("notes.read: want map args")
	}
	id, _ := a["id"].(string)

	notesMu.Lock()
	body, found := notes[id]
	notesMu.Unlock()

	if !found {
		return nil, fmt.Errorf("notes.read: %q not found", id)
	}
	return map[string]any{"id": id, "body": body}, nil
}

func notesListFn(_ context.Context, _ tools.Context, _ any) (any, error) {
	notesMu.Lock()
	ids := make([]string, 0, len(notes))
	for id := range notes {
		ids = append(ids, id)
	}
	notesMu.Unlock()
	return map[string]any{"ids": ids, "count": len(ids)}, nil
}
