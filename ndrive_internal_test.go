package ndrive

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/wyx0k/ndrive/fs"
)

// failFSConfig 是一个初始化失败的 FS 配置。
type failFSConfig struct{}

func (failFSConfig) NewFileService(context.Context) (fs.FileService, error) {
	return nil, errBoom
}

// bareFSConfig 返回一个不含约定目录的文件系统（用来验证 fail-fast）。
type bareFSConfig struct{}

func (bareFSConfig) NewFileService(context.Context) (fs.FileService, error) {
	return &bareFS{}, nil
}

// bareFS 是一个最小实现：Stat 一律查不到（约定目录不存在）。
type bareFS struct{}

func (b *bareFS) List(context.Context, string, int, int) (fs.ListPage, error) {
	return fs.ListPage{}, errors.New("not implemented")
}
func (b *bareFS) Stat(context.Context, string) (fs.FileEntry, error) {
	return fs.FileEntry{}, errors.New("not found")
}
func (b *bareFS) Read(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}
func (b *bareFS) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (b *bareFS) Search(context.Context, string, bool) ([]fs.FileEntry, error) {
	return nil, errors.New("not implemented")
}
func (b *bareFS) Write(context.Context, string, []byte) (fs.FileEntry, error) {
	return fs.FileEntry{}, errors.New("not implemented")
}
func (b *bareFS) WriteStream(context.Context, string, io.Reader) (fs.FileEntry, error) {
	return fs.FileEntry{}, errors.New("not implemented")
}
func (b *bareFS) Edit(context.Context, string, string, string, time.Time) (fs.FileEntry, error) {
	return fs.FileEntry{}, errors.New("not implemented")
}
func (b *bareFS) Delete(context.Context, string, bool) error {
	return errors.New("not implemented")
}

// TestNewFileServiceRejectsNil 验证 FS 配置必填。
func TestNewFileServiceRejectsNil(t *testing.T) {
	if _, err := newFileService(context.Background(), nil); err == nil {
		t.Fatal("want error for nil fs config")
	}
}

// TestNewFileServicePropagatesInitError 验证 fs 实现初始化失败时直接失败
// （不构造半残工作区）。
func TestNewFileServicePropagatesInitError(t *testing.T) {
	_, err := newFileService(context.Background(), failFSConfig{})
	if err == nil {
		t.Fatal("want error from failing fs init")
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("want wrapped boom error, got %v", err)
	}
}

// TestNewFileServiceRequiresConventionDirs 验证缺少 /.tmp、/.mem 的 fs 实现
// 在装配阶段就被拒（fail-fast），而不是等到上层读写时才出问题。
func TestNewFileServiceRequiresConventionDirs(t *testing.T) {
	if _, err := newFileService(context.Background(), bareFSConfig{}); err == nil {
		t.Fatal("want error for fs without convention dirs")
	}
}

// errBoom 是 failFSConfig 返回的哨兵错误。
var errBoom = errors.New("boom")
