package ntd_test

import (
	"context"
	"testing"

	"github.com/wyx0k/ndrive"
)

func TestProcedureRegisterAndCall(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	ws.SetPolicy(ctx, ntd.W)

	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "docs",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}

	bm.RegisterProcedure(ntd.Procedure{
		Name:        "create-note",
		Description: "创建一个带标题的笔记文件",
		Schema: map[string]any{
			"type":     "object",
			"required": []string{"path", "title"},
			"properties": map[string]any{
				"path":  map[string]any{"type": "string"},
				"title": map[string]any{"type": "string"},
				"body":  map[string]any{"type": "string"},
			},
		},
		Fn: func(ctx context.Context, r ntd.BizmodelResolver, args any) (any, error) {
			m := args.(map[string]any)
			path, _ := m["path"].(string)
			title, _ := m["title"].(string)
			body, _ := m["body"].(string)
			bm, err := r.GetBizmodel(ctx, "docs")
			if err != nil {
				return nil, err
			}
			return bm.Call(ctx, "file-fm-write", path, map[string]any{
				"withfm": true,
				"fm":     map[string]any{"title": title},
				"body":   body,
			})
		},
	})

	procs := bm.Procedures()
	if len(procs) != 1 || procs[0].Name != "create-note" {
		t.Fatalf("want 1 procedure got %v", procs)
	}

	ownerCtx := ws.Context(ctx)
	_, err = bm.CallProcedure(ownerCtx, "create-note", map[string]any{
		"path":  "/notes/hello.md",
		"title": "Hello",
		"body":  "# Hello\n",
	})
	if err != nil {
		t.Fatalf("CallProcedure: %v", err)
	}

	briefs, err := bm.Tree().ListWithBrief(ownerCtx, "/notes")
	if err != nil {
		t.Fatalf("ListWithBrief: %v", err)
	}
	if len(briefs) == 0 {
		t.Fatal("expected file written by procedure")
	}
}

func TestProcedureNotFound(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	ws.SetPolicy(ctx, ntd.W)

	bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID: "docs", TypeName: ntd.BackendLocalFile,
		Config: ntd.LocalFileConfig{RootDir: t.TempDir()},
	})

	_, err := bm.CallProcedure(ws.Context(ctx), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown procedure")
	}
}

func TestProcedurePolicyEnforced(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	ws.SetPolicy(ctx, ntd.R) // 只读

	bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID: "docs", TypeName: ntd.BackendLocalFile,
		Config: ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	bm.RegisterProcedure(ntd.Procedure{
		Name: "write-something",
		Fn: func(ctx context.Context, r ntd.BizmodelResolver, args any) (any, error) {
			bm, err := r.GetBizmodel(ctx, "docs")
			if err != nil {
				return nil, err
			}
			return bm.Call(ctx, "file-write", "/test.md", map[string]any{"body": "x"})
		},
	})

	_, err := bm.CallProcedure(ws.Context(ctx), "write-something", nil)
	if err == nil {
		t.Fatal("expected policy denial under R mode")
	}
}

func TestProcedureBusEvents(t *testing.T) {
	ctx := context.Background()
	ws := openFreshWorkspace(t, t.Name())
	ws.SetPolicy(ctx, ntd.W)

	bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID: "docs", TypeName: ntd.BackendLocalFile,
		Config: ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	bm.RegisterProcedure(ntd.Procedure{
		Name: "noop",
		Fn:   func(ctx context.Context, r ntd.BizmodelResolver, args any) (any, error) { return nil, nil },
	})

	started := make(chan ntd.ProcedureEvent, 1)
	done := make(chan ntd.ProcedureEvent, 1)
	ws.Bus().Subscribe("procedure.start", func(p any) { started <- p.(ntd.ProcedureEvent) })
	ws.Bus().Subscribe("procedure.done", func(p any) { done <- p.(ntd.ProcedureEvent) })

	bm.CallProcedure(ws.Context(ctx), "noop", nil)

	select {
	case e := <-started:
		if e.Name != "noop" {
			t.Fatalf("want noop got %s", e.Name)
		}
	default:
		t.Fatal("expected procedure.start event")
	}
	select {
	case e := <-done:
		if e.Name != "noop" {
			t.Fatalf("want noop got %s", e.Name)
		}
	default:
		t.Fatal("expected procedure.done event")
	}
}
