// local_file_example.go：v5 自定义 localfile plugin 注册示例。
//
// v5 哲学：ndrive 不内置任何 backend；用户用 Plugin 接口自己写。
// 这里写一个简单的 store.localfile.read / store.localfile.write plugin。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
	"github.com/wyx0k/ndrive/tools/builtin"
)

func main() {
	ctx := context.Background()
	ws, err := ndrive.OpenWorkspace(ctx, "demo", "demo-user", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: "/tmp/ndrive-demo.db"},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	// opt-in: 注册 util.echo
	if err := ws.Register(builtin.Echo()); err != nil {
		panic(err)
	}

	// 自定义 store.localfile.write
	if err := ws.Register(tools.SimplePluginDef{
		ID_:          "store.localfile.write",
		Description_: "写入本地文件",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path", "body"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
				"body": map[string]any{"type": "string"},
			},
		},
		Read_: false, // 写
		Fn_: func(_ context.Context, ws tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			body, _ := a["body"].(string)
			if err := os.WriteFile(path, []byte(body), 0644); err != nil {
				return nil, err
			}
			// 用 ws 的 bus 发事件
			ws.Bus().Publish("file.written", map[string]any{"path": path})
			return map[string]any{"ok": true, "path": path}, nil
		},
	}); err != nil {
		panic(err)
	}

	// 自定义 store.localfile.read（依赖 write 不存在，所以单独注册）
	if err := ws.Register(tools.SimplePluginDef{
		ID_:          "store.localfile.read",
		Description_: "读取本地文件",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
		Read_: true, // 读
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "body": string(data)}, nil
		},
	}); err != nil {
		panic(err)
	}

	// 演示：把 ws 的 policy 设成 W（写放行）
	if err := ws.SetPolicy(ctx, "demo-user", permission.W); err != nil {
		panic(err)
	}

	// 调用 write
	v, err := ws.API().Call(ctx, "store.localfile.write", map[string]any{
		"path": "/tmp/ndrive-demo.txt",
		"body": "hello v5",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("write result:", v)

	// 调用 read
	v, err = ws.API().Call(ctx, "store.localfile.read", map[string]any{
		"path": "/tmp/ndrive-demo.txt",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("read result:", v)
}
