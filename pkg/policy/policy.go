package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wyx0k/ndrive/pkg/bus"
	"github.com/wyx0k/ndrive/pkg/meta"
)

var ErrPolicyDenied = errors.New("policy denied")

type Mode int

const (
	RAsk Mode = iota
	R
	WAsk
	W
)

type Binding struct {
	WorkspaceID string
	OwnerID     string
	Mode        Mode
}

type AskPayload struct {
	WorkspaceID string
	OwnerID     string
	Op          string
	Path        string
	Preview     string
}

type AskResponse struct {
	Approved bool
}

const kindPrefix = "policy_binding/"

func bindingKey(wsID, ownerID string) string {
	return fmt.Sprintf("%s%s/%s", kindPrefix, wsID, ownerID)
}

func Set(ctx context.Context, store meta.Store, b Binding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return store.Put(ctx, bindingKey(b.WorkspaceID, b.OwnerID), data)
}

func Get(ctx context.Context, store meta.Store, wsID, ownerID string) (Mode, error) {
	rec, err := store.Get(ctx, bindingKey(wsID, ownerID))
	if err != nil {
		return R, err
	}
	if rec.Value == nil {
		return R, nil
	}
	var b Binding
	if err := json.Unmarshal(rec.Value, &b); err != nil {
		return R, err
	}
	return b.Mode, nil
}

func List(ctx context.Context, store meta.Store, wsID string) ([]Binding, error) {
	prefix := fmt.Sprintf("%s%s/", kindPrefix, wsID)
	recs, err := store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]Binding, 0, len(recs))
	for _, rec := range recs {
		var b Binding
		if err := json.Unmarshal(rec.Value, &b); err != nil {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func Delete(ctx context.Context, store meta.Store, wsID, ownerID string) error {
	return store.Delete(ctx, bindingKey(wsID, ownerID))
}

type Checker interface {
	Check(ctx context.Context, op string) error
}

// Bus は policy が使う bus の最小インタフェース
type Bus interface {
	Request(ctx context.Context, topic string, req any) (any, error)
}

type checker struct {
	store   meta.Store
	wsID    string
	ownerID string
	bus     Bus
}

func NewChecker(store meta.Store, wsID, ownerID string, b Bus) Checker {
	return &checker{store: store, wsID: wsID, ownerID: ownerID, bus: b}
}

func isWriteOp(op string) bool {
	op = strings.ToLower(op)
	return !strings.HasPrefix(op, "read") && !strings.HasPrefix(op, "list")
}

func (c *checker) Check(ctx context.Context, op string) error {
	mode, err := Get(ctx, c.store, c.wsID, c.ownerID)
	if err != nil {
		return err
	}
	write := isWriteOp(op)
	switch mode {
	case W:
		return nil
	case WAsk:
		if !write {
			return nil
		}
		return c.ask(ctx, op)
	case R:
		if write {
			return fmt.Errorf("%w: op=%s mode=R", ErrPolicyDenied, op)
		}
		return nil
	case RAsk:
		return c.ask(ctx, op)
	}
	return nil
}

func (c *checker) ask(ctx context.Context, op string) error {
	resp, err := c.bus.Request(ctx, "policy.ask", AskPayload{
		WorkspaceID: c.wsID,
		OwnerID:     c.ownerID,
		Op:          op,
	})
	if errors.Is(err, bus.ErrNoSubscriber) {
		return fmt.Errorf("%w: op=%s (no subscriber)", ErrPolicyDenied, op)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPolicyDenied, err)
	}
	ar, ok := resp.(AskResponse)
	if !ok || !ar.Approved {
		return fmt.Errorf("%w: op=%s denied by user", ErrPolicyDenied, op)
	}
	return nil
}
