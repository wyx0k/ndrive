package main

import (
	"context"
	"fmt"
	"log"

	"ntd"
)

func main() {
	ctx := context.Background()

	// 1. 打开 workspace（sqlite meta，localfile backend）
	ws, err := ntd.OpenWorkspace(ctx, "ws-demo", "owner-alice", ntd.Config{
		Meta: ntd.SqliteConfig{Path: "./demo.db"},
	})
	if err != nil {
		log.Fatal("open workspace:", err)
	}
	defer ws.Close()

	fmt.Println("workspace:", ws.ID(), "owner:", ws.OwnerID())

	// 2. 创建 localfile bizmodel
	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "notes",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: "./notes-store"},
	})
	if err != nil {
		bm, err = ws.GetBizmodel(ctx, "notes")
		if err != nil {
			log.Fatal("get bizmodel:", err)
		}
	}
	fmt.Println("bizmodel:", bm.ID(), "type:", bm.Type().Name)

	// 3. 设置 policy = W
	if err := ws.SetPolicy(ctx, ntd.W); err != nil {
		log.Fatal("set policy:", err)
	}
	fmt.Println("policy set to W")

	// 4. 注入 wsID/ownerID 到 ctx
	ownerCtx := ws.Context(ctx)

	// 5. 创建目录
	if _, err := bm.Call(ownerCtx, "dir-mkdir", "/notes", nil); err != nil {
		log.Println("mkdir /notes:", err)
	} else {
		fmt.Println("created dir /notes")
	}

	// 6. 写入带 frontmatter 的 markdown 文件
	if _, err := bm.Call(ownerCtx, "file-fm-write", "/notes/hello.md", map[string]any{
		"withfm": true,
		"fm": map[string]any{
			"title": "Hello ntd",
			"tags":  []any{"demo", "ntd"},
		},
		"body": "# Hello ntd\n\n这是一个演示文件。\n",
	}); err != nil {
		log.Fatal("file-fm-write:", err)
	}
	fmt.Println("wrote /notes/hello.md")

	// 7. ListWithBrief
	briefs, err := bm.Tree().ListWithBrief(ownerCtx, "/notes")
	if err != nil {
		log.Fatal("list:", err)
	}
	fmt.Println("\nbriefs:")
	for _, b := range briefs {
		fmt.Printf("  [%s] %s  title=%q  preview=%q\n", b.SubKind, b.Path, b.Title, b.Preview)
	}

	// 8. Search
	results, err := bm.Tree().Search(ownerCtx, "*hello*")
	if err != nil {
		log.Fatal("search:", err)
	}
	fmt.Println("\nsearch *hello*:")
	for _, b := range results {
		fmt.Printf("  [%s] %s  title=%q\n", b.SubKind, b.Path, b.Title)
	}
}
