package localfs

import (
	"context"
	"fmt"
	"io"
	"time"

	filewatcher "github.com/larsartmann/go-filewatcher/v2"
	"gopkg.in/yaml.v3"

	"github.com/wyx0k/ndrive/tools"
	"github.com/wyx0k/ndrive/tools/builtin"
)

// containerID 是 localfs 容器的命名空间；容器内 tool 的完整 id 都是
// containerID + "." + 下面的短 id。
const containerID tools.PluginID = "store.localfs"

// 容器内 tool 短 id（不带 store.localfs. 前缀）。
const (
	toolList          = "list"
	toolRead          = "read"
	toolSearch        = "search"
	toolStat          = "stat"
	toolWrite         = "write"
	toolWriteMarkdown = "write-markdown"
	toolEdit          = "edit"
	toolDelete        = "delete"
	// toolOpen / toolWriteStream 是 Internal tool：不出现在 Tools()/Funcs()，
	// 只能被其他 plugin 通过 ToolsAPI.Call("store.localfs.xxx", ...) 内部调用
	// （如 zip 解析器需要 io.ReadCloser 流式读取；上游转发 HTTP body 需要
	// 流式写入，而不是先整体读入内存再传 JSON 参数）。
	toolOpen        = "open-stream"
	toolWriteStream = "write-stream"
)

// topic 命名：localfs 自定义事件，不带引擎的 "plugin." 前缀。
const (
	topicCreated = "file.created"
	topicWritten = "file.modified"
	topicRemoved = "file.removed"
	topicRenamed = "file.renamed"
)

// Config 是注册 localfs plugin 时的初始配置（传给 Register 的 cfg 参数）。
type Config struct {
	// Root 是本地文件系统根目录（沙盒边界）。必填。
	Root string
	// DebounceInterval 是文件变更事件的防抖间隔；<= 0 时使用 500ms 默认值。
	DebounceInterval time.Duration
}

// localfsPlugin 是 store.localfs 容器：容器内所有 tool（list/read/search/stat/
// write/write-markdown/edit/delete/open-stream/write-stream）共享同一个 *FS
// 实例和同一个文件监听 watcher。容器本身实现 ServicePlugin（Service()
// 返回 *FS）和 StatefulPlugin（启停文件监听），不再像旧版本那样随意挂在某一
// 个 tool 上。
type localfsPlugin struct {
	fs      *FS
	watcher *filewatcher.Watcher
}

// NewPlugin 构造 store.localfs 容器（1 个 tools.Plugin，内含一组 tool），
// 供 ws.Register 注册。root 是沙盒根目录。
func NewPlugin(root string, opts ...Option) (tools.Plugin, error) {
	fs, err := New(root, opts...)
	if err != nil {
		return nil, err
	}
	return &localfsPlugin{fs: fs}, nil
}

func (p *localfsPlugin) ID() tools.PluginID { return containerID }

func (p *localfsPlugin) Tools() []tools.Tool {
	return []tools.Tool{
		p.listTool(),
		p.readTool(),
		p.searchTool(),
		p.statTool(),
		p.openTool(),
		p.writeTool(),
		p.writeStreamTool(),
		p.writeMarkdownTool(),
		p.editTool(),
		p.deleteTool(),
	}
}

func (p *localfsPlugin) listTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolList,
		Description_: "分页列出目录下的条目（不递归），.md 文件带前 100 字简介",
		Schema_: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "description": "相对根目录的路径，留空表示根目录"},
				"page":     map[string]any{"type": "integer", "description": "页码，从 1 开始，默认 1"},
				"pageSize": map[string]any{"type": "integer", "description": "每页大小，默认 20"},
			},
		},
		Read_: true,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, _ := args.(map[string]any)
			path, _ := a["path"].(string)
			page := toInt(a["page"])
			pageSize := toInt(a["pageSize"])
			return p.fs.List(ctx, path, page, pageSize)
		},
	}
}

func (p *localfsPlugin) readTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolRead,
		Description_: "一次性读取整个文件内容（超过大小上限会报错，大文件请用 open 流式读取）",
		Schema_: map[string]any{
			"type":       "object",
			"required":   []string{"path"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
		Read_: true,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			data, err := p.fs.Read(ctx, path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "body": string(data)}, nil
		},
	}
}

func (p *localfsPlugin) searchTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolSearch,
		Description_: "按文件名搜索（可选同时搜索文本内容）",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query":         map[string]any{"type": "string"},
				"contentSearch": map[string]any{"type": "boolean", "description": "是否同时匹配文件内容，默认 false"},
			},
		},
		Read_: true,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			query, _ := a["query"].(string)
			contentSearch, _ := a["contentSearch"].(bool)
			return p.fs.Search(ctx, query, contentSearch)
		},
	}
}

func (p *localfsPlugin) statTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolStat,
		Description_: "查询单个路径的元信息（是否存在、是文件还是目录、大小、修改时间）",
		Schema_: map[string]any{
			"type":       "object",
			"required":   []string{"path"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
		Read_: true,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			return p.fs.Stat(ctx, path)
		},
	}
}

// openTool 返回 io.ReadCloser，只给其他 plugin 内部调用（Internal_ = true），
// 不出现在 Tools()/Funcs() 的 LLM/UI 视图里——LLM 不适合直接拿到一个流式句柄。
func (p *localfsPlugin) openTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolOpen,
		Description_: "按路径打开文件，返回流式 io.ReadCloser（内部工具，不对外暴露）",
		Schema_: map[string]any{
			"type":       "object",
			"required":   []string{"path"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
		Read_:     true,
		Internal_: true,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			return p.fs.Open(ctx, path)
		},
	}
}

// writeTool 创建或整体覆盖文件内容。
func (p *localfsPlugin) writeTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolWrite,
		Description_: "创建或整体覆盖文件内容（父目录不存在会自动创建）",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path", "body"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
				"body": map[string]any{"type": "string"},
			},
		},
		Read_: false,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			body, _ := a["body"].(string)
			return p.fs.Write(ctx, path, []byte(body))
		},
	}
}

// writeStreamTool 是 Internal tool：供其他 plugin 原生调用，直接传入
// io.Reader（不走 JSON 序列化），用于流式写入大内容。
func (p *localfsPlugin) writeStreamTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolWriteStream,
		Description_: "流式写入文件（内部工具，不对外暴露，args[\"reader\"] 需为 io.Reader）",
		Schema_: map[string]any{
			"type":       "object",
			"required":   []string{"path", "reader"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
		Read_:     false,
		Internal_: true,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			r, ok := a["reader"].(io.Reader)
			if !ok {
				return nil, fmt.Errorf("localfs: args[\"reader\"] must be io.Reader, got %T", a["reader"])
			}
			return p.fs.WriteStream(ctx, path, r)
		},
	}
}

// writeMarkdownTool 写 markdown 文件，withfm=true 时在正文前拼 YAML frontmatter。
func (p *localfsPlugin) writeMarkdownTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolWriteMarkdown,
		Description_: "写 markdown 文件，可选带 YAML frontmatter（--- ... ---）",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path", "body"},
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"withfm": map[string]any{"type": "boolean", "description": "是否写入 frontmatter，默认 false"},
				"fm":     map[string]any{"type": "object", "description": "frontmatter 字段，withfm=true 时生效"},
				"body":   map[string]any{"type": "string", "description": "正文内容"},
			},
		},
		Read_: false,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			body, _ := a["body"].(string)
			withfm, _ := a["withfm"].(bool)

			content := body
			if withfm {
				fm, _ := a["fm"].(map[string]any)
				frontmatter, err := yaml.Marshal(fm)
				if err != nil {
					return nil, fmt.Errorf("localfs: marshal frontmatter: %w", err)
				}
				content = "---\n" + string(frontmatter) + "---\n" + body
			}
			return p.fs.Write(ctx, path, []byte(content))
		},
	}
}

// editTool 对已存在文件做乐观并发的 old/new 字符串替换。
func (p *localfsPlugin) editTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolEdit,
		Description_: "对文件内容做 old→new 的字符串替换（old 必须恰好出现一次）；modTime 必须与文件当前修改时间一致，否则报错（乐观并发校验）",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path", "old", "new", "modTime"},
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"old":     map[string]any{"type": "string", "description": "待替换的原文，必须在文件中恰好出现一次"},
				"new":     map[string]any{"type": "string", "description": "替换后的新文本"},
				"modTime": map[string]any{"type": "string", "description": "调用方读取到的文件修改时间（RFC3339），用于乐观并发校验"},
			},
		},
		Read_: false,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			old, _ := a["old"].(string)
			newText, _ := a["new"].(string)
			modTimeStr, _ := a["modTime"].(string)

			expectedModTime, err := time.Parse(time.RFC3339Nano, modTimeStr)
			if err != nil {
				return nil, fmt.Errorf("localfs: parse modTime %q: %w", modTimeStr, err)
			}
			return p.fs.Edit(ctx, path, old, newText, expectedModTime)
		},
	}
}

// deleteTool 删除文件或目录。删除目录必须显式传 recursive=true，否则报错
// （防止"删一个文件"顺手把整棵子树删掉）；删除沙盒根目录本身一律拒绝。
func (p *localfsPlugin) deleteTool() tools.Tool {
	return tools.SimplePluginDef{
		ID_:          toolDelete,
		Description_: "删除文件或目录（删除目录必须显式传 recursive=true，否则报错；不能删除根目录本身）",
		Schema_: map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "相对根目录的路径"},
				"recursive": map[string]any{"type": "boolean", "description": "递归删除目录及其全部内容，默认 false"},
			},
		},
		Read_: false,
		Fn_: func(ctx context.Context, _ tools.Context, args any) (any, error) {
			a, ok := args.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("localfs: expected map args, got %T", args)
			}
			path, _ := a["path"].(string)
			recursive, _ := a["recursive"].(bool)
			if err := p.fs.Delete(ctx, path, recursive); err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "deleted": true}, nil
		},
	}
}

// Service 返回 FS 本身（实现 builtin.FileService），供其他 plugin 原生调用。
func (p *localfsPlugin) Service() any { return p.fs }

// StartPlugin 启动文件系统监听，变更通过 ws.Bus().Publish 发 file.* 事件。
// cfg 若为 Config 类型则使用其中的 DebounceInterval；否则使用默认 500ms。
func (p *localfsPlugin) StartPlugin(ws tools.Context, cfg any) error {
	debounce := 500 * time.Millisecond
	if c, ok := cfg.(Config); ok && c.DebounceInterval > 0 {
		debounce = c.DebounceInterval
	}

	w, err := filewatcher.New([]string{p.fs.Root()}, filewatcher.WithDebounce(debounce))
	if err != nil {
		return fmt.Errorf("localfs: create watcher: %w", err)
	}
	events, err := w.Watch(context.Background())
	if err != nil {
		_ = w.Close()
		return fmt.Errorf("localfs: start watch: %w", err)
	}
	p.watcher = w

	go func() {
		for ev := range events {
			publishFileEvent(ws, p.fs, ev)
		}
	}()
	return nil
}

// StopPlugin 停止文件系统监听，关闭底层 watcher 资源。
func (p *localfsPlugin) StopPlugin() error {
	if p.watcher == nil {
		return nil
	}
	return p.watcher.Close()
}

// publishFileEvent 把 filewatcher.Event 转成 localfs 的 file.* 事件并发到 bus。
// path 转换为相对 root 的沙盒路径，不泄露宿主机绝对路径。
func publishFileEvent(ws tools.Context, fs *FS, ev filewatcher.Event) {
	if ws == nil || ws.Bus() == nil {
		return
	}
	topic := ""
	switch ev.Op {
	case filewatcher.Create:
		topic = topicCreated
	case filewatcher.Write:
		topic = topicWritten
	case filewatcher.Remove:
		topic = topicRemoved
	case filewatcher.Rename:
		topic = topicRenamed
	default:
		return
	}
	// 事件路径必须仍在 root 内——否则 toRel 会算出带 ".." 的相对路径，把
	// 宿主机目录结构漏到 bus 上。
	if !fs.contains(ev.Path) {
		return
	}
	ws.Bus().Publish(topic, map[string]any{
		"path":     fs.toRel(ev.Path),
		"is_dir":   ev.IsDir,
		"size":     ev.Size,
		"mod_time": ev.ModTime,
	})
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

var _ tools.Plugin = (*localfsPlugin)(nil)
var _ tools.ServicePlugin = (*localfsPlugin)(nil)
var _ tools.StatefulPlugin = (*localfsPlugin)(nil)
var _ builtin.FileService = (*FS)(nil)
var _ io.ReadCloser = (io.ReadCloser)(nil)
