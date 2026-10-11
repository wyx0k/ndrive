package localfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rjeczalik/notify"
	"ntd/pkg/backend"
	"ntd/pkg/subkind"
	"ntd/pkg/tree"
)

const Name = "local_file"

type Config struct {
	RootDir string
}

func (Config) IsBackendConfig() {}

var dirKind = subkind.Kind{
	Name:        "dir",
	Description: "目录节点",
	Ops:         []string{"dir-mkdir", "dir-rm"},
}

var fileKind = subkind.Kind{
	Name:        "file",
	Description: "文件节点",
	Ops:         []string{"file-write", "file-fm-write", "file-read"},
}

type localfileBackend struct{}

func New() backend.Backend { return &localfileBackend{} }

func (b *localfileBackend) Name() string { return Name }

func (b *localfileBackend) SubKinds() []subkind.Kind {
	return []subkind.Kind{dirKind, fileKind}
}

func (b *localfileBackend) OpsForSubKind(kind string) []tree.NamedOp {
	switch kind {
	case "dir":
		return dirOps()
	case "file":
		return fileOps()
	}
	return nil
}

func (b *localfileBackend) ConfigFromJSON(data []byte) (backend.Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (b *localfileBackend) NewTree(cfg backend.Config) (tree.Tree, error) {
	c, ok := cfg.(Config)
	if !ok {
		return nil, errors.New("localfile: invalid config type")
	}
	if err := os.MkdirAll(c.RootDir, 0755); err != nil {
		return nil, err
	}
	return &localTree{root: c.RootDir}, nil
}

type localTree struct {
	root   string
	stopCh chan struct{}
}

func (t *localTree) abs(path string) string {
	return filepath.Join(t.root, filepath.FromSlash(path))
}

func (t *localTree) Get(ctx context.Context, path string) (*tree.Node, error) {
	abs := t.abs(path)
	info, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sk := "file"
	if info.IsDir() {
		sk = "dir"
	}
	return &tree.Node{Path: path, SubKind: sk}, nil
}

func (t *localTree) List(ctx context.Context, prefix string) ([]*tree.Node, error) {
	abs := t.abs(prefix)
	var nodes []*tree.Node
	err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(t.root, p)
		rel = "/" + filepath.ToSlash(rel)
		if rel == prefix {
			return nil // 跳过 prefix 节点自身
		}
		sk := "file"
		if d.IsDir() {
			sk = "dir"
		}
		nodes = append(nodes, &tree.Node{Path: rel, SubKind: sk})
		return nil
	})
	return nodes, err
}

func (t *localTree) ListWithBrief(ctx context.Context, prefix string) ([]*tree.Brief, error) {
	abs := t.abs(prefix)
	var briefs []*tree.Brief
	err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(t.root, p)
		rel = "/" + filepath.ToSlash(rel)
		if rel == prefix {
			return nil // 跳过 prefix 节点自身
		}
		sk := "file"
		if d.IsDir() {
			sk = "dir"
		}
		preview := ""
		if !d.IsDir() {
			preview = filePreview(p)
		}
		briefs = append(briefs, &tree.Brief{
			Path:    rel,
			SubKind: sk,
			Title:   d.Name(),
			Preview: preview,
		})
		return nil
	})
	return briefs, err
}

func (t *localTree) Search(ctx context.Context, pattern string) ([]*tree.Brief, error) {
	var briefs []*tree.Brief
	err := filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(t.root, p)
		rel = "/" + filepath.ToSlash(rel)
		if rel == "/" {
			return nil // 跳过根节点
		}
		name := d.Name()
		if !matchPattern(pattern, rel) && !matchPattern(pattern, name) {
			return nil
		}
		sk := "file"
		if d.IsDir() {
			sk = "dir"
		}
		preview := ""
		if !d.IsDir() {
			preview = filePreview(p)
		}
		briefs = append(briefs, &tree.Brief{
			Path:    rel,
			SubKind: sk,
			Title:   name,
			Preview: preview,
		})
		return nil
	})
	return briefs, err
}

func matchPattern(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)
	pfx := strings.HasPrefix(pattern, "*")
	sfx := strings.HasSuffix(pattern, "*")
	core := strings.Trim(pattern, "*")
	switch {
	case pfx && sfx:
		return strings.Contains(s, core)
	case pfx:
		return strings.HasSuffix(s, core)
	case sfx:
		return strings.HasPrefix(s, core)
	default:
		return s == pattern
	}
}

func (t *localTree) Delete(ctx context.Context, path string) error {
	return os.RemoveAll(t.abs(path))
}

func filePreview(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 200)
	n, _ := f.Read(buf)
	s := string(buf[:n])
	if strings.HasPrefix(s, "---") {
		end := strings.Index(s[3:], "---")
		if end >= 0 {
			s = s[3+end+3:]
		}
	}
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func dirOps() []tree.NamedOp {
	return []tree.NamedOp{
		{
			Name:        "dir-mkdir",
			Description: "创建目录",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			UpdateOp:    tree.UpdateCreate,
			Op: func(ctx context.Context, t tree.Tree, args tree.OpArgs) (any, error) {
				return nil, os.MkdirAll(t.(*localTree).abs(args.Path), 0755)
			},
		},
		{
			Name:        "dir-rm",
			Description: "删除目录",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			UpdateOp:    tree.UpdateDelete,
			Op: func(ctx context.Context, t tree.Tree, args tree.OpArgs) (any, error) {
				return nil, os.RemoveAll(t.(*localTree).abs(args.Path))
			},
		},
	}
}

func fileOps() []tree.NamedOp {
	return []tree.NamedOp{
		{
			Name:        "file-write",
			Description: "写入文件内容",
			Schema: map[string]any{
				"type":     "object",
				"required": []string{"body"},
				"properties": map[string]any{
					"body": map[string]any{"type": "string"},
				},
			},
			UpdateOp: tree.UpdateWrite,
			Op: func(ctx context.Context, t tree.Tree, args tree.OpArgs) (any, error) {
				m, _ := args.Args.(map[string]any)
				body, _ := m["body"].(string)
				return nil, os.WriteFile(t.(*localTree).abs(args.Path), []byte(body), 0644)
			},
		},
		{
			Name:        "file-fm-write",
			Description: "写入带 frontmatter 的 markdown 文件",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"withfm": map[string]any{"type": "boolean"},
					"fm":     map[string]any{"type": "object"},
					"body":   map[string]any{"type": "string"},
				},
			},
			UpdateOp: tree.UpdateWrite,
			Op: func(ctx context.Context, t tree.Tree, args tree.OpArgs) (any, error) {
				lt := t.(*localTree)
				m, _ := args.Args.(map[string]any)
				body, _ := m["body"].(string)
				content := body
				if withfm, _ := m["withfm"].(bool); withfm {
					if fm, ok := m["fm"].(map[string]any); ok {
						content = serializeFrontmatter(fm) + body
					}
				}
				if err := os.MkdirAll(filepath.Dir(lt.abs(args.Path)), 0755); err != nil {
					return nil, err
				}
				return nil, os.WriteFile(lt.abs(args.Path), []byte(content), 0644)
			},
		},
		{
			Name:        "file-read",
			Description: "读取文件内容",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
			// 读操作不设 UpdateOp
			Op: func(ctx context.Context, t tree.Tree, args tree.OpArgs) (any, error) {
				data, err := os.ReadFile(t.(*localTree).abs(args.Path))
				return string(data), err
			},
		},
	}
}

func serializeFrontmatter(fm map[string]any) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	for k, v := range fm {
		fmt.Fprintf(&sb, "%s: %s\n", k, formatValue(v))
	}
	sb.WriteString("---\n")
	return sb.String()
}

func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []string:
		return "[" + strings.Join(val, ", ") + "]"
	case []any:
		parts := make([]string, len(val))
		for i, e := range val {
			parts[i] = formatValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return ""
	}
}

// Watch 启动文件系统监听，变更事件通过 pub 发布到 "tree.update"
func (t *localTree) Watch(pub tree.Publisher) error {
	ch := make(chan notify.EventInfo, 64)
	// "..." 表示递归监听，notify 内部用系统原生递归 API，无需手动 WalkDir
	if err := notify.Watch(t.root+"/...", ch, notify.All); err != nil {
		return err
	}
	t.stopCh = make(chan struct{})
	go t.watchLoop(pub, ch)
	return nil
}

func (t *localTree) watchLoop(pub tree.Publisher, ch chan notify.EventInfo) {
	defer notify.Stop(ch)
	for {
		select {
		case <-t.stopCh:
			return
		case ei, ok := <-ch:
			if !ok {
				return
			}
			rel, _ := filepath.Rel(t.root, ei.Path())
			rel = "/" + filepath.ToSlash(rel)
			pub.Publish("tree.update", tree.UpdateEvent{Path: rel, Op: notifyEventToUpdateOp(ei.Event())})
		}
	}
}

func (t *localTree) StopWatch() {
	if t.stopCh != nil {
		close(t.stopCh)
		t.stopCh = nil
	}
}

func notifyEventToUpdateOp(e notify.Event) tree.UpdateOp {
	switch e {
	case notify.Create:
		return tree.UpdateCreate
	case notify.Remove:
		return tree.UpdateDelete
	case notify.Rename:
		return tree.UpdateRename
	default:
		return tree.UpdateWrite
	}
}
