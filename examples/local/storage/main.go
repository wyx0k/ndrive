// storage 演示工作区文件系统（fs-api）怎么被可调用单元使用。
//
// v5 哲学：工作区自带一个文件系统（fs.FileService），实现方由用户选择
// （当前只有本地目录 fs-local）。可调用单元（func / tool）只依赖 fs-api
// 接口，不关心底层是本地目录、内存还是对象存储。
//
// 这里写两个最简单的 store.localfile.{read,write} 单元，内部通过
// ws.FS() 拿到工作区文件系统来读写。
//
// 运行：go run ./examples/local/storage
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/fs/local"
	"github.com/wyx0k/ndrive/meta/sqlite"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-storage.db"
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)
	wsRoot := "./ndrive-storage-ws"
	_ = os.RemoveAll(wsRoot)
	defer os.RemoveAll(wsRoot)

	ws, err := ndrive.OpenWorkspace(ctx, "demo", "demo-user", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: dbPath},
		FS:   local.Config{Path: wsRoot},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	// 工作区文件系统：可调用单元只认这个接口。
	fsvc := ws.FS()

	// 自定义 store.localfile.write：写工作区文件。
	if err := ws.Register(tools.ToolDef{
		ID_:          "store.localfile.write",
		Description_: "写入工作区文件",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path", "body"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
				"body": map[string]any{"type": "string"},
			},
		},
		Read_: false,
		Fn_: func(_ context.Context, ws tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			body, _ := a["body"].(string)
			entry, err := fsvc.Write(context.Background(), path, []byte(body))
			if err != nil {
				return nil, err
			}
			ws.Bus().Publish("file.written", map[string]any{"path": path})
			return map[string]any{"ok": true, "path": entry.Path, "size": entry.Size}, nil
		},
	}); err != nil {
		panic(err)
	}

	// 自定义 store.localfile.read：读工作区文件。
	if err := ws.Register(tools.ToolDef{
		ID_:          "store.localfile.read",
		Description_: "读取工作区文件",
		Schema_: map[string]any{
			"type":       "object",
			"required":   []string{"path"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
		Read_: true,
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			data, err := fsvc.Read(context.Background(), path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "body": string(data)}, nil
		},
	}); err != nil {
		panic(err)
	}

	// 把 policy 设成 ModeWriteOnly（写放行），否则默认 ModeWriteAsk 下写操作会被拒绝/询问。
	if err := ws.SetPolicy(ctx, "demo-user", permission.ModeWriteOnly); err != nil {
		panic(err)
	}

	target := "notes/hello.txt"

	v, err := ws.API().Call(ctx, "store.localfile.write", map[string]any{
		"path": target,
		"body": "hello ndrive",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("write result:", v)

	// ModeWriteOnly 模式下读操作会被拒绝；切到 ModeWriteAsk 再读回来。
	if err := ws.SetPolicy(ctx, "demo-user", permission.ModeWriteAsk); err != nil {
		panic(err)
	}

	v, err = ws.API().Call(ctx, "store.localfile.read", map[string]any{"path": target})
	if err != nil {
		panic(err)
	}
	fmt.Println("read result:", v)
}
