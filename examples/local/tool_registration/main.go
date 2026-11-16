// tool_registration 演示 ndrive 的工具注册体系。
//
// 覆盖：
//   - ToolDef 声明式注册（最常用方式）
//   - 自定义 Tool 接口实现（手写方法）
//   - DependsOn 工具间依赖（见 DependentTool）+ 跨工具调用（通过 ws.API().Call）
//   - RegisterAll 批量注册（含依赖校验 + 拓扑排序）
//   - Resolve 查看注册表的拓扑执行顺序
//   - Tools() 投影（LLM 视角）+ Call 唯一执行入口
//
// 运行：go run ./examples/local/tool_registration
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/fs/local"
	"github.com/wyx0k/ndrive/meta/sqlite"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-tools.db"
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)
	wsRoot := "./ndrive-tools-ws"
	_ = os.RemoveAll(wsRoot)
	defer os.RemoveAll(wsRoot)

	ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: dbPath},
		FS:   local.Config{Path: wsRoot},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	// 本示例全是读操作；设成 ModeWriteAsk（默认档）让读操作直接放行。
	must(ws.SetPolicy(ctx, "alice", permission.ModeWriteAsk))

	// 1. ToolDef：声明式注册，最常用的方式。
	must(ws.Register(tools.ToolDef{
		ID_:          "util.now",
		Description_: "返回当前时间（RFC3339）",
		Schema_:      map[string]any{"type": "object"},
		Read_:        true,
		Fn_: func(_ context.Context, _ tools.Context, _ any) (any, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	}))

	// 2. 自定义 Tool 接口：手写 ID/Description/Schema/Read/Call 五个方法
	//    （DependsOn 是可选的 DependentTool 接口，这里没有依赖所以不实现）。
	must(ws.Register(newGreeterTool()))

	// 3. DependsOn：func.calc 依赖 util.now，内部通过 ws.API().Call 调用依赖。
	must(ws.Register(tools.ToolDef{
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
		Read_:      true,
		DependsOn_: []tools.ToolID{"util.now"},
		Fn_:        calcFn,
	}))

	// 4. RegisterAll：批量注册一组互相依赖的工具（含依赖校验 + 拓扑排序）。
	batch := []tools.Tool{
		tools.ToolDef{
			ID_:          "func.notes.create",
			Description_: "创建一条 note",
			Read_:        false,
			Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
				return args, nil
			},
		},
		tools.ToolDef{
			ID_:          "func.notes.list",
			Description_: "列出所有 note",
			Read_:        true,
			DependsOn_:   []tools.ToolID{"func.notes.create"},
			Fn_: func(_ context.Context, _ tools.Context, _ any) (any, error) {
				return []string{}, nil
			},
		},
	}
	must(ws.API().RegisterAll(batch))

	// 5. Resolve：查看注册表的拓扑执行顺序（依赖在前）。
	order, err := ws.API().Resolve()
	must(err)
	fmt.Printf("拓扑序：%v\n", order)

	// 6. Tools()：LLM 视角投影（id + description + json schema）。
	llmView, _ := json.MarshalIndent(ws.API().Tools(), "", "  ")
	fmt.Printf("Tools()（LLM 视角）：\n%s\n", llmView)

	// 7. Call 是唯一的执行入口：AI 调用（拿着 Tools() 的 schema 生成参数）和
	//    直接函数调用走的是同一条路径，权限门控与事件广播对两者一视同仁。
	v, err := ws.API().Call(ctx, "func.calc", map[string]any{"a": 10, "b": 4, "op": "mul"})
	must(err)
	fmt.Printf("Call 直接调用 func.calc：10 * 4 = %v\n", v)

	// 8. 跨工具调用 + 依赖链验证。
	v, err = ws.API().Call(ctx, "func.calc", map[string]any{"a": 10, "b": 0, "op": "div"})
	fmt.Printf("除零错误：v=%v err=%v\n", v, err)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}

// greeterTool 演示手写 Tool 接口（不用 ToolDef）；直接注册即可，
// 它的 ID() 就是注册表里的键。
type greeterTool struct {
	calls int
}

func newGreeterTool() *greeterTool { return &greeterTool{} }

func (g *greeterTool) ID() tools.ToolID    { return "greeter.greet" }
func (g *greeterTool) Description() string { return "打招呼：name → hello, <name>!" }
func (g *greeterTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"required":   []string{"name"},
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
	}
}
func (g *greeterTool) Read() bool { return true }
func (g *greeterTool) Call(_ context.Context, _ tools.Context, args any) (any, error) {
	g.calls++
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("greeter: want map args")
	}
	name, _ := a["name"].(string)
	return fmt.Sprintf("hello, %s! (#%d)", name, g.calls), nil
}

// calcFn 内部调用依赖工具 util.now，演示跨工具调用。
func calcFn(ctx context.Context, ws tools.Context, args any) (any, error) {
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("calc: want map args")
	}
	x := asFloat(a["a"])
	y := asFloat(a["b"])
	op, _ := a["op"].(string)

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

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
