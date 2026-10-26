// tool_registration 演示 ndrive 的插件（tool）注册体系。
//
// 覆盖：
//   - SimplePluginDef 声明式注册（最常用方式）
//   - 自定义 Tool 接口实现（手写 7 个方法）
//   - DependsOn 插件依赖 + 跨插件调用（通过 ws.API().Call）
//   - RegisterAll 批量注册（含依赖校验 + 拓扑排序）
//   - Resolve 查看注册表的拓扑执行顺序
//   - Tools() / Funcs() 双视图投影（LLM 视角 vs UI 视角）
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
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-tools.db"
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

	// 本示例全是读操作；设成 RAsk（默认档）让读操作直接放行。
	must(ws.SetPolicy(ctx, "alice", permission.RAsk))

	// 1. SimplePluginDef：声明式注册，最常用的方式。
	must(ws.Register(tools.SimplePluginDef{
		ID_:          "util.now",
		Description_: "返回当前时间（RFC3339）",
		Schema_:      map[string]any{"type": "object"},
		Read_:        true,
		Fn_: func(_ context.Context, _ tools.Context, _ any) (any, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	}, nil))

	// 2. 自定义 Plugin 接口：手写 ID/Description/Schema/DependsOn/Read/Call 六个方法。
	must(ws.Register(newGreeterPlugin(), nil))

	// 3. DependsOn：func.calc 依赖 util.now，内部通过 ws.API().Call 调用依赖。
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
		Read_:      true,
		DependsOn_: []tools.PluginID{"util.now"},
		Fn_:        calcFn,
	}, nil))

	// 4. RegisterAll：批量注册一组互相依赖的插件（含依赖校验 + 拓扑排序）。
	batch := []tools.Plugin{
		tools.SimplePluginDef{
			ID_:          "func.notes.create",
			Description_: "创建一条 note",
			Read_:        false,
			Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
				return args, nil
			},
		},
		tools.SimplePluginDef{
			ID_:          "func.notes.list",
			Description_: "列出所有 note",
			Read_:        true,
			DependsOn_:   []tools.PluginID{"func.notes.create"},
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

	// 7. Funcs()：UI 视角投影，Call 已 bind 到当前 workspace，可直接调用。
	for _, f := range ws.API().Funcs() {
		if f.ID == "func.calc" {
			v, err := f.Call(ctx, map[string]any{"a": 10, "b": 4, "op": "mul"})
			must(err)
			fmt.Printf("Funcs() 直接调用 func.calc：10 * 4 = %v\n", v)
		}
	}

	// 8. 跨插件调用 + 依赖链验证。
	v, err := ws.API().Call(ctx, "func.calc", map[string]any{"a": 10, "b": 0, "op": "div"})
	fmt.Printf("除零错误：v=%v err=%v\n", v, err)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}

// greeterPlugin 演示手写 Tool 接口（不用 SimplePluginDef）；注册时 Register
// 自动把它包装成单 tool Plugin 容器。
type greeterPlugin struct {
	calls int
}

func newGreeterPlugin() *greeterPlugin { return &greeterPlugin{} }

func (g *greeterPlugin) ID() tools.PluginID  { return "greeter.greet" }
func (g *greeterPlugin) Description() string { return "打招呼：name → hello, <name>!" }
func (g *greeterPlugin) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"required":   []string{"name"},
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
	}
}
func (g *greeterPlugin) DependsOn() []tools.PluginID { return nil }
func (g *greeterPlugin) Read() bool                  { return true }
func (g *greeterPlugin) Internal() bool              { return false }
func (g *greeterPlugin) Call(_ context.Context, _ tools.Context, args any) (any, error) {
	g.calls++
	a, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("greeter: want map args")
	}
	name, _ := a["name"].(string)
	return fmt.Sprintf("hello, %s! (#%d)", name, g.calls), nil
}

// calcFn 内部调用依赖插件 util.now，演示跨插件调用。
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
