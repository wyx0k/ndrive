package mysql

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ntd/pkg/meta"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	MaxOpenConns int
	MaxIdleConns int
}

type record struct {
	Key   string `gorm:"primarykey;size:512"`
	Value []byte `gorm:"type:longblob"`
}

type store struct {
	db *gorm.DB
}

func New(ctx context.Context, cfg Config) (meta.Store, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
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
	escaped := strings.ReplaceAll(prefix, "%", "\\%")
	escaped = strings.ReplaceAll(escaped, "_", "\\_")
	err := s.db.WithContext(ctx).Where("key LIKE ?", escaped+"%").Find(&rows).Error
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
