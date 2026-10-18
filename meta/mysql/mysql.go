// Package mysql 实现 meta.Store。
package mysql

import (
	"context"
	"errors"
	"fmt"

	"github.com/wyx0k/ndrive/meta"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config mysql 配置。
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
	Key   string `gorm:"primarykey"`
	Value []byte
}

type store struct {
	db *gorm.DB
}

// New 创建 mysql store。
func New(ctx context.Context, cfg Config) (meta.Store, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
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
	if err := db.WithContext(ctx).AutoMigrate(&record{}); err != nil {
		return nil, err
	}
	return &store{db: db}, nil
}

func (s *store) Get(ctx context.Context, key string) (meta.Record, error) {
	var r record
	err := s.db.WithContext(ctx).Where("key = ?", key).First(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return meta.Record{Key: key}, nil
		}
		return meta.Record{}, err
	}
	return meta.Record{Key: r.Key, Value: r.Value}, nil
}

func (s *store) Put(ctx context.Context, key string, value []byte) error {
	r := record{Key: key, Value: value}
	return s.db.WithContext(ctx).Save(&r).Error
}

func (s *store) Delete(ctx context.Context, key string) error {
	return s.db.WithContext(ctx).Where("key = ?", key).Delete(&record{}).Error
}

func (s *store) List(ctx context.Context, prefix string) ([]meta.Record, error) {
	var rs []record
	q := s.db.WithContext(ctx)
	if prefix != "" {
		q = q.Where("key LIKE ?", prefix+"%")
	}
	if err := q.Find(&rs).Error; err != nil {
		return nil, err
	}
	out := make([]meta.Record, 0, len(rs))
	for _, r := range rs {
		out = append(out, meta.Record{Key: r.Key, Value: r.Value})
	}
	return out, nil
}

func (s *store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
