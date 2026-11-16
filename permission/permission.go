// Package permission 提供 ndrive 的 4 档权限门控。
//
// v5 设计要点：
//   - 改名 policy → permission（全仓库统一）
//   - owner_id → user_id
//   - Checker 拆成 CheckRead / CheckWrite（tool.Read() 路由）
//   - 每 user 一个 mode（Binding 按 (wsID, userID) 唯一索引）
//   - AskPayload.Preview 允许空字符串
//   - 无订阅者返 ErrPermissionDenied
//   - ctx 超时由 permission.ask handler 内部 listen，permission 收到 ErrRequestTimeout 转译为 ErrPermissionDenied
//   - WorkspaceStore 接口注入给 NewChecker（封装 Set/Get/List/Delete）
package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wyx0k/ndrive/bus"
)

// ErrPermissionDenied 是 permission 拒绝时的统一错误。
var ErrPermissionDenied = errors.New("permission denied")

// Mode 权限档位：决定「读」「写」两类操作各自的处置方式。
//
// 四档语义（读 | 写）：
//
//	ModeReadOnly    放行 | 拒绝    只读：写操作一律拒绝
//	ModeWriteAsk    放行 | 询问    读直接放行；写发 permission.ask 询问
//	ModeAskAll      询问 | 询问    读写都要人工确认
//	ModeWriteOnly   拒绝 | 放行    只写：读操作一律拒绝
//
// 这四档**不是一条严格度链**——ModeReadOnly 与 ModeWriteAsk 都放行读，
// ModeAskAll 与 ModeWriteAsk 都要问写。选哪一档取决于两件事：读要不要、
// 写要不要问。
//
// 数值（iota）与历史持久化数据一一对应，改名不改值：
// 0=写询问 1=只读 2=全询问 3=只写。
type Mode int

const (
	// ModeWriteAsk 读放行 / 写询问。零值，也是最常用的默认档。
	ModeWriteAsk Mode = iota
	// ModeReadOnly 读放行 / 写拒绝。
	ModeReadOnly
	// ModeAskAll 读写都询问。
	ModeAskAll
	// ModeWriteOnly 读拒绝 / 写放行。
	ModeWriteOnly
)

// String 实现 fmt.Stringer；用于日志和持久化展示。
func (m Mode) String() string {
	switch m {
	case ModeWriteAsk:
		return "WriteAsk"
	case ModeReadOnly:
		return "ReadOnly"
	case ModeAskAll:
		return "AskAll"
	case ModeWriteOnly:
		return "WriteOnly"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// MarshalJSON / UnmarshalJSON 用数字（iota 值）持久化，避免 iota 顺序变动破坏历史数据。
func (m Mode) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(m))
}

func (m *Mode) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*m = Mode(n)
	return nil
}

// Binding 是 (wsID, userID) → Mode 的绑定关系。
type Binding struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Mode        Mode   `json:"mode"`
}

// AskPayload 是 permission.ask 请求的 payload。
type AskPayload struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Op          string `json:"op"`
	Preview     string `json:"preview"` // v5: 允许空字符串
}

// AskResponse 是 UI 端 Handle("permission.ask") 的返值。
type AskResponse struct {
	Approved bool `json:"approved"`
}

// WorkspaceStore 是 permission 包对 binding 持久化的最小接口。
// 实现方：ndrive 根包通过 internal/meta 暴露。
type WorkspaceStore interface {
	GetBinding(ctx context.Context, wsID, userID string) (Binding, error)
	SetBinding(ctx context.Context, b Binding) error
	DeleteBinding(ctx context.Context, wsID, userID string) error
	ListBindings(ctx context.Context, wsID string) ([]Binding, error)
}

// Checker 是 permission 门控的核心接口。
// v5 拆成 CheckRead / CheckWrite 两个方法（tool.Read() 路由）。
type Checker interface {
	CheckRead(ctx context.Context, op string) error
	CheckWrite(ctx context.Context, op string) error
}

// checker 默认实现。
type checker struct {
	store  WorkspaceStore
	wsID   string
	userID string
	bus    bus.Bus
}

// NewChecker 构造一个 Checker 实例。
// 每次 NewChecker 绑定 (wsID, userID)；同 ws 多 user 调多次 NewChecker。
func NewChecker(store WorkspaceStore, wsID, userID string, b bus.Bus) Checker {
	return &checker{store: store, wsID: wsID, userID: userID, bus: b}
}

// CheckRead 走 mode 对 read 的判定。
func (c *checker) CheckRead(ctx context.Context, op string) error {
	return c.check(ctx, op, true)
}

// CheckWrite 走 mode 对 write 的判定。
func (c *checker) CheckWrite(ctx context.Context, op string) error {
	return c.check(ctx, op, false)
}

// check 统一逻辑：read=true 是读，read=false 是写。
//
// fail-closed：拿不到 binding（用户没配权限）或 mode 越界，一律拒绝。
func (c *checker) check(ctx context.Context, op string, read bool) error {
	b, err := c.store.GetBinding(ctx, c.wsID, c.userID)
	if err != nil {
		// 没有 binding = 没授权 = 默认拒绝（而不是把底层错误透出去，
		// 让调用方分不清"没权限"和"存储挂了"）。
		return fmt.Errorf("%w: op=%s (no binding for user %s: %v)", ErrPermissionDenied, op, c.userID, err)
	}
	write := !read
	switch b.Mode {
	case ModeWriteOnly:
		if write {
			return nil
		}
		return fmt.Errorf("%w: op=%s mode=WriteOnly (read op not allowed)", ErrPermissionDenied, op)
	case ModeAskAll:
		// 读写都要问。
		return c.ask(ctx, op)
	case ModeReadOnly:
		if write {
			return fmt.Errorf("%w: op=%s mode=ReadOnly (write op not allowed)", ErrPermissionDenied, op)
		}
		return nil
	case ModeWriteAsk:
		if !write {
			return nil
		}
		return c.ask(ctx, op)
	}
	// 未知 mode（脏数据 / 历史越界）→ 拒绝。
	return fmt.Errorf("%w: op=%s unknown mode %d", ErrPermissionDenied, op, int(b.Mode))
}

// ask 发 permission.ask 请求到 bus，等 UI 端 AskResponse。
// 错误处理：
//   - 无订阅者 → ErrPermissionDenied
//   - 超时（ErrRequestTimeout）→ ErrPermissionDenied
//   - handler 返非 AskResponse / Approved=false → ErrPermissionDenied
func (c *checker) ask(ctx context.Context, op string) error {
	resp, err := c.bus.Request(ctx, "permission.ask", AskPayload{
		WorkspaceID: c.wsID,
		UserID:      c.userID,
		Op:          op,
		Preview:     "",
	})
	if errors.Is(err, bus.ErrNoSubscriber) {
		return fmt.Errorf("%w: op=%s (no subscriber)", ErrPermissionDenied, op)
	}
	if errors.Is(err, bus.ErrRequestTimeout) {
		return fmt.Errorf("%w: op=%s (timeout)", ErrPermissionDenied, op)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}
	ar, ok := resp.(AskResponse)
	if !ok || !ar.Approved {
		return fmt.Errorf("%w: op=%s denied by user", ErrPermissionDenied, op)
	}
	return nil
}
