// storage 演示如何给 ndrive 接入自定义存储后端。
//
// v5 哲学：ndrive 不内置任何存储 backend；用户用 Plugin 接口自己写
// store.* 插件，接到任意后端（本地文件、对象存储、数据库……）。
// 这里写一个最简单的本地文件 store.localfile.{read,write} 插件。
//
// 运行：go run ./examples/local/storage
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-storage.db"
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)

	ws, err := ndrive.OpenWorkspace(ctx, "demo", "demo-user", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: dbPath},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	// 自定义 store.localfile.write：写入本地文件。
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
		Read_: false, // 写操作
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
			ws.Bus().Publish("file.written", map[string]any{"path": path})
			return map[string]any{"ok": true, "path": path}, nil
		},
	}); err != nil {
		panic(err)
	}

	// 自定义 store.localfile.read：读取本地文件。
	if err := ws.Register(tools.SimplePluginDef{
		ID_:          "store.localfile.read",
		Description_: "读取本地文件",
		Schema_: map[string]any{
			"type":       "object",
			"required":   []string{"path"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
		Read_: true, // 读操作
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

	// 把 policy 设成 W（写放行），否则默认 RAsk 下写操作会被拒绝/询问。
	if err := ws.SetPolicy(ctx, "demo-user", permission.W); err != nil {
		panic(err)
	}

	target := "./ndrive-storage-demo.txt"
	defer os.Remove(target)

	v, err := ws.API().Call(ctx, "store.localfile.write", map[string]any{
		"path": target,
		"body": "hello ndrive",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("write result:", v)

	// W 模式下读操作会被拒绝；切到 RAsk 再读回来。
	if err := ws.SetPolicy(ctx, "demo-user", permission.RAsk); err != nil {
		panic(err)
	}

	v, err = ws.API().Call(ctx, "store.localfile.read", map[string]any{"path": target})
	if err != nil {
		panic(err)
	}
	fmt.Println("read result:", v)
}
