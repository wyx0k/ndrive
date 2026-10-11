# Backend 扩展

## 内置 Backend

| 常量 | 存储 |
|---|---|
| `BackendLocalFile` | 本地文件系统 |
| `BackendLocalSQLite` | 本地 SQLite |
| `BackendLocalBadger` | 本地 BadgerDB |
| `BackendRemoteSQL` | 远端 SQL（MySQL 等） |
| `BackendRemoteMongo` | 远端 MongoDB |

## 扩展 Backend

实现 `ntd.BackendProvider` 接口并注册，无需导入任何内部包：

```go
type MyConfig struct{ Dir string }
func (MyConfig) IsBackendConfig() {}

type MyBackend struct{}
func (MyBackend) Name() string                                          { return "my_backend" }
func (MyBackend) SubKinds() []ntd.SubKind                              { ... }
func (MyBackend) OpsForSubKind(kind string) []ntd.ExternalOp           { ... }
func (MyBackend) NewTree(cfg ntd.BackendConfig) (ntd.Tree, error)      { ... }
func (MyBackend) ConfigFromJSON(data []byte) (ntd.BackendConfig, error) { ... }

func init() {
    ntd.RegisterBackend(MyBackend{})
}
```

## 自定义 Backend 示例（WebSearch）

以搜索结果作为只读树节点为例，展示如何把外部数据源接入 ntd。

**定义 Config 和 Backend**

```go
package websearch

import (
    "context"
    "encoding/json"
    "fmt"
    "ntd"
)

type Config struct {
    APIKey   string
    Endpoint string
}

func (Config) IsBackendConfig() {}

type Backend struct{}

func (Backend) Name() string { return "web_search" }

func (Backend) SubKinds() []ntd.SubKind {
    return []ntd.SubKind{
        {Name: "result", Description: "搜索结果节点", Ops: []string{"result-search", "result-open"}},
    }
}

func (Backend) OpsForSubKind(kind string) []ntd.ExternalOp {
    if kind != "result" {
        return nil
    }
    return []ntd.ExternalOp{
        {
            Name:        "result-search",
            Description: "执行搜索，把结果写入树",
            Schema: map[string]any{
                "type":     "object",
                "required": []string{"query"},
                "properties": map[string]any{
                    "query": map[string]any{"type": "string"},
                },
            },
            Op: func(ctx context.Context, t ntd.Tree, args ntd.OpArgs) (any, error) {
                m, _ := args.Args.(map[string]any)
                query, _ := m["query"].(string)
                // 调用搜索 API，把结果写入树（通过 t 的具体实现）
                return doSearch(ctx, query)
            },
        },
    }
}

func (Backend) NewTree(cfg ntd.BackendConfig) (ntd.Tree, error) {
    c, ok := cfg.(Config)
    if !ok {
        return nil, fmt.Errorf("websearch: invalid config")
    }
    return &searchTree{apiKey: c.APIKey, endpoint: c.Endpoint}, nil
}

func (Backend) ConfigFromJSON(data []byte) (ntd.BackendConfig, error) {
    var c Config
    return c, json.Unmarshal(data, &c)
}
```

**注册并使用**

```go
func init() {
    ntd.RegisterBackend(websearch.Backend{})
}

// 创建 bizmodel
bm, _ := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
    ID:       "search",
    TypeName: "web_search",
    Config:   websearch.Config{APIKey: "sk-xxx", Endpoint: "https://api.example.com"},
})

// 执行搜索 op
ownerCtx := ws.Context(ctx)
results, _ := bm.Call(ownerCtx, "result-search", "/search/go-generics", map[string]any{
    "query": "Go generics best practices",
})

// 读取搜索结果树
briefs, _ := bm.Tree().ListWithBrief(ownerCtx, "/search")
```

下游只需 `import "github.com/wyx0k/ndrive"`，不需要接触任何引擎内部包。
