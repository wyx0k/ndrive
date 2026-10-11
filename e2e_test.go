package ntd_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wyx0k/ndrive"
)

// TestE2E 模拟下游应用完整使用流程：
// workspace → bizmodel → policy → procedure → tree → bus 事件
func TestE2E(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// 1. 打开 workspace
	ws, err := ntd.OpenWorkspace(ctx, "e2e-ws", "owner-alice", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer ws.Close()

	// 2. 创建 bizmodel
	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "notes",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: dir},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}

	// 3. 收集所有 bus 事件
	type busEvent struct {
		topic   string
		payload any
	}
	collected := make(chan busEvent, 64)
	var unsubs []func()
	for _, topic := range []string{"tree.update", "procedure.start", "procedure.done", "procedure.error"} {
		_, u := ws.Bus().Subscribe(topic, func(p any) {
			select {
			case collected <- busEvent{topic: topic, payload: p}:
			default:
			}
		})
		unsubs = append(unsubs, u)
	}

	// 4. WAsk policy + 自动批准 handler
	if err := ws.SetPolicy(ctx, ntd.WAsk); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
		t.Logf("policy.ask: auto-approved req=%v", req)
		return ntd.AskResponse{Approved: true}, nil
	})

	ownerCtx := ws.Context(ctx)

	// 5. 注册 Procedure：批量创建笔记
	bm.RegisterProcedure(ntd.Procedure{
		Name:        "bulk-create-notes",
		Description: "批量创建笔记文件",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir":   map[string]any{"type": "string"},
				"notes": map[string]any{"type": "array"},
			},
		},
		Fn: func(ctx context.Context, r ntd.BizmodelResolver, args any) (any, error) {
			m := args.(map[string]any)
			baseDir, _ := m["dir"].(string)
			notes, _ := m["notes"].([]any)
			bm, err := r.GetBizmodel(ctx, "notes")
			if err != nil {
				return nil, err
			}
			if _, err := bm.Call(ctx, "dir-mkdir", baseDir, nil); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", baseDir, err)
			}
			for _, n := range notes {
				note := n.(map[string]any)
				title, _ := note["title"].(string)
				body, _ := note["body"].(string)
				path := baseDir + "/" + strings.ReplaceAll(strings.ToLower(title), " ", "-") + ".md"
				if _, err := bm.Call(ctx, "file-fm-write", path, map[string]any{
					"withfm": true,
					"fm":     map[string]any{"title": title},
					"body":   body,
				}); err != nil {
					return nil, fmt.Errorf("write %s: %w", path, err)
				}
			}
			return len(notes), nil
		},
	})

	// 6. 执行 procedure
	result, err := bm.CallProcedure(ownerCtx, "bulk-create-notes", map[string]any{
		"dir": "/journal",
		"notes": []any{
			map[string]any{"title": "Day One", "body": "First day.\n"},
			map[string]any{"title": "Day Two", "body": "Second day.\n"},
			map[string]any{"title": "Day Three", "body": "Third day.\n"},
		},
	})
	if err != nil {
		t.Fatalf("CallProcedure: %v", err)
	}
	if result.(int) != 3 {
		t.Fatalf("want 3 notes created got %v", result)
	}

	// 7. 验证树内容
	briefs, err := bm.Tree().ListWithBrief(ownerCtx, "/journal")
	if err != nil {
		t.Fatalf("ListWithBrief: %v", err)
	}
	if len(briefs) != 3 {
		t.Fatalf("want 3 briefs got %d: %v", len(briefs), briefs)
	}
	t.Log("briefs:")
	for _, b := range briefs {
		t.Logf("  [%s] %s preview=%q", b.SubKind, b.Path, b.Preview)
	}

	// 8. 搜索
	results, err := bm.Tree().Search(ownerCtx, "*day*")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 3 {
		t.Fatalf("want >=3 search results got %d", len(results))
	}
	t.Logf("search *day*: %d results", len(results))

	// 9. 验证 bus 事件 — 先取消订阅，再排空缓冲
	for _, u := range unsubs {
		u()
	}
	eventCounts := map[string]int{}
draining:
	for {
		select {
		case e := <-collected:
			eventCounts[e.topic]++
			t.Logf("bus event: %s %v", e.topic, e.payload)
		default:
			break draining
		}
	}
	if eventCounts["procedure.start"] != 1 {
		t.Errorf("want 1 procedure.start got %d", eventCounts["procedure.start"])
	}
	if eventCounts["procedure.done"] != 1 {
		t.Errorf("want 1 procedure.done got %d", eventCounts["procedure.done"])
	}
	if eventCounts["tree.update"] == 0 {
		t.Error("want at least 1 tree.update event")
	}
	t.Logf("tree.update events: %d", eventCounts["tree.update"])

	// 10. GetBizmodel 重新加载后仍可用
	bm2, err := ws.GetBizmodel(ctx, "notes")
	if err != nil {
		t.Fatalf("GetBizmodel: %v", err)
	}
	briefs2, err := bm2.Tree().ListWithBrief(ownerCtx, "/journal")
	if err != nil {
		t.Fatalf("ListWithBrief after reload: %v", err)
	}
	if len(briefs2) != 3 {
		t.Fatalf("want 3 briefs after reload got %d", len(briefs2))
	}

	// 11. DeleteBizmodel
	if err := ws.DeleteBizmodel(ctx, "notes"); err != nil {
		t.Fatalf("DeleteBizmodel: %v", err)
	}
	if _, err := ws.GetBizmodel(ctx, "notes"); err == nil {
		t.Fatal("expected error after delete")
	}
}
