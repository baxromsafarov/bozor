// Package storage инкапсулирует объектное хранилище оригиналов медиа (MinIO/S3).
package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config — параметры подключения к объектному хранилищу.
type Config struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string
	UseSSL        bool
	Bucket        string
	PublicBaseURL string
}

// Store кладёт оригиналы медиа в бакет и строит публичные URL.
type Store struct {
	client        *minio.Client
	bucket        string
	publicBaseURL string
}

// New создаёт клиент объектного хранилища.
func New(cfg Config) (*Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: клиент MinIO: %w", err)
	}
	return &Store{client: client, bucket: cfg.Bucket, publicBaseURL: cfg.PublicBaseURL}, nil
}

// EnsureBucket создаёт бакет, если его ещё нет (идемпотентно).
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("storage: проверка бакета: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("storage: создание бакета: %w", err)
	}
	return nil
}

// Bucket возвращает имя бакета.
func (s *Store) Bucket() string { return s.bucket }

// Put загружает объект в бакет.
func (s *Store) Put(ctx context.Context, objectKey string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("storage: загрузка объекта: %w", err)
	}
	return nil
}

// Get читает объект из бакета целиком (для обработки воркером).
func (s *Store) Get(ctx context.Context, objectKey string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: получение объекта: %w", err)
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("storage: чтение объекта: %w", err)
	}
	return data, nil
}

// Remove удаляет объект из бакета (для отката/очистки).
func (s *Store) Remove(ctx context.Context, objectKey string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: удаление объекта: %w", err)
	}
	return nil
}

// PresignedURL возвращает временную (presigned) ссылку GET на объект со сроком
// жизни ttl. Используется для доступа владельца к оригиналу (превью — публично).
func (s *Store) PresignedURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, objectKey, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("storage: presigned-ссылка: %w", err)
	}
	return u.String(), nil
}

// PublicURL возвращает публичный URL объекта (бакет отдаётся download-политикой).
func (s *Store) PublicURL(objectKey string) string {
	return s.publicBaseURL + "/" + objectKey
}

// Ping проверяет доступность хранилища (для readyz).
func (s *Store) Ping(ctx context.Context) error {
	if _, err := s.client.BucketExists(ctx, s.bucket); err != nil {
		return fmt.Errorf("storage: недоступно: %w", err)
	}
	return nil
}
