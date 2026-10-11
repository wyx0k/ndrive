package ntd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ntd/pkg/backend"
	pkgbus "ntd/pkg/bus"
	pkgctx "ntd/pkg/ctx"
	pkgmeta "ntd/pkg/meta"
	"ntd/pkg/policy"
)

var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrOwnerMismatch     = errors.New("workspace owner_id mismatch")
	ErrBizmodelNotFound  = errors.New("bizmodel not found")
	ErrBizmodelExists    = errors.New("bizmodel exists")
)

const wsKindPrefix  = "workspace/"
const bizKindPrefix = "bizmodel/"

type wsMeta struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
}

type bizMetaRecord struct {
	ID       string          `json:"id"`
	WSID     string          `json:"ws_id"`
	TypeName string          `json:"type_name"`
	Config   json.RawMessage `json:"config"`
}

// Workspace 工作区句柄（唯一对外入口）
type Workspace struct {
	id      string
	ownerID string
	store   pkgmeta.Store
	bus     pkgbus.Bus
}

func (w *Workspace) ID() string      { return w.id }
func (w *Workspace) OwnerID() string { return w.ownerID }
func (w *Workspace) Close() error    { return nil }

// Context 返回注入了 wsID 和 ownerID 的 ctx，用于后续 Call/Tree 操作
func (w *Workspace) Context(ctx context.Context) context.Context {
	return pkgctx.WithOwner(pkgctx.WithWorkspace(ctx, w.id), w.ownerID)
}

// SetPolicy 设置当前 owner 在此 workspace 的权限
func (w *Workspace) SetPolicy(ctx context.Context, mode PolicyMode) error {
	return policy.Set(ctx, w.store, policy.Binding{
		WorkspaceID: w.id,
		OwnerID:     w.ownerID,
		Mode:        policy.Mode(mode),
	})
}

// GetPolicy 获取当前 owner 的权限
func (w *Workspace) GetPolicy(ctx context.Context) (PolicyMode, error) {
	m, err := policy.Get(ctx, w.store, w.id, w.ownerID)
	return PolicyMode(m), err
}

// ListPolicies 列出 workspace 下所有 policy binding
func (w *Workspace) ListPolicies(ctx context.Context) ([]policy.Binding, error) {
	return policy.List(ctx, w.store, w.id)
}

// DeletePolicy 删除当前 owner 的 policy binding
func (w *Workspace) DeletePolicy(ctx context.Context) error {
	return policy.Delete(ctx, w.store, w.id, w.ownerID)
}

// CreateBizmodel 创建并持久化一个 bizmodel
func (w *Workspace) CreateBizmodel(ctx context.Context, opts BizmodelOpts) (*Bizmodel, error) {
	key := bizKindPrefix + w.id + "/" + opts.ID
	existing, err := w.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if existing.Value != nil {
		return nil, fmt.Errorf("%w: %s", ErrBizmodelExists, opts.ID)
	}
	be, ok := backend.Get(opts.TypeName)
	if !ok {
		return nil, fmt.Errorf("unknown backend type: %s", opts.TypeName)
	}
	resolvedCfg, err := resolveBackendConfig(opts.Config)
	if err != nil {
		return nil, err
	}
	t, err := be.NewTree(resolvedCfg)
	if err != nil {
		return nil, fmt.Errorf("backend init failed: %w", err)
	}
	cfgBytes, err := json.Marshal(resolvedCfg)
	if err != nil {
		return nil, err
	}
	rec := bizMetaRecord{ID: opts.ID, WSID: w.id, TypeName: opts.TypeName, Config: cfgBytes}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	if err := w.store.Put(ctx, key, data); err != nil {
		return nil, err
	}
	return newBizmodel(opts.ID, be, t, w.store, w.id, w.ownerID, w.bus), nil
}

// GetBizmodel 按 ID 获取已有 bizmodel
func (w *Workspace) GetBizmodel(ctx context.Context, bizID string) (*Bizmodel, error) {
	key := bizKindPrefix + w.id + "/" + bizID
	rec, err := w.store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if rec.Value == nil {
		return nil, fmt.Errorf("%w: %s", ErrBizmodelNotFound, bizID)
	}
	return w.loadBizmodel(rec.Value)
}

// ListBizModels 列出 workspace 下所有 bizmodel
func (w *Workspace) ListBizModels(ctx context.Context) ([]*Bizmodel, error) {
	prefix := bizKindPrefix + w.id + "/"
	recs, err := w.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Bizmodel, 0, len(recs))
	for _, rec := range recs {
		bm, err := w.loadBizmodel(rec.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, bm)
	}
	return out, nil
}

// DeleteBizmodel 删除一个 bizmodel
func (w *Workspace) DeleteBizmodel(ctx context.Context, bizID string) error {
	return w.store.Delete(ctx, bizKindPrefix+w.id+"/"+bizID)
}

func (w *Workspace) loadBizmodel(data []byte) (*Bizmodel, error) {
	var rec bizMetaRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	be, ok := backend.Get(rec.TypeName)
	if !ok {
		return nil, fmt.Errorf("unknown backend type: %s", rec.TypeName)
	}
	cfg, err := be.ConfigFromJSON(rec.Config)
	if err != nil {
		return nil, fmt.Errorf("%s ConfigFromJSON: %w", rec.TypeName, err)
	}
	t, err := be.NewTree(cfg)
	if err != nil {
		return nil, err
	}
	return newBizmodel(rec.ID, be, t, w.store, w.id, w.ownerID, w.bus), nil
}
