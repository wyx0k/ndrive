package sqlite

import (
	"context"
	"errors"
	"strings"

	"github.com/wyx0k/ndrive/pkg/meta"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	Path        string
	MaxOpenConn int
}

type record struct {
	Key   string `gorm:"primarykey"`
	Value []byte
}

type store struct {
	db *gorm.DB
}

func New(ctx context.Context, cfg Config) (meta.Store, error) {
	db, err := gorm.Open(sqlite.Open(cfg.Path), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConn > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConn)
	}
	if err := db.AutoMigrate(&record{}); err != nil {
		return nil, err
	}
	return &store{db: db}, nil
}

func (s *store) Get(ctx context.Context, key string) (meta.Record, error) {
	var r record
	err := s.db.WithContext(ctx).First(&r, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return meta.Record{}, nil
	}
	if err != nil {
		return meta.Record{}, err
	}
	return meta.Record{Key: r.Key, Value: r.Value}, nil
}

func (s *store) Put(ctx context.Context, key string, value []byte) error {
	return s.db.WithContext(ctx).Save(&record{Key: key, Value: value}).Error
}

func (s *store) Delete(ctx context.Context, key string) error {
	return s.db.WithContext(ctx).Delete(&record{}, "key = ?", key).Error
}

func (s *store) List(ctx context.Context, prefix string) ([]meta.Record, error) {
	var rows []record
	err := s.db.WithContext(ctx).Where("key LIKE ?", strings.ReplaceAll(prefix, "%", "\\%")+"%").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]meta.Record, len(rows))
	for i, r := range rows {
		out[i] = meta.Record{Key: r.Key, Value: r.Value}
	}
	return out, nil
}

func (s *store) Close() error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
