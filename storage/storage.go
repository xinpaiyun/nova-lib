package storage

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/xinpaiyun/nova-lib/config"
)

var defaultStore Store

// UploadOptions 描述上传对象的业务归属。
type UploadOptions struct {
	TenantID uint64
	FileType string
	RefID    uint64
}

// UploadResult 描述上传后的访问地址和对象键。
type UploadResult struct {
	URL       string `json:"url"`
	ObjectKey string `json:"objectKey"`
	Storage   string `json:"storage"`
}

// Store 定义文件存储适配器接口。
type Store interface {
	Upload(ctx context.Context, file io.Reader, filename string, contentLength int64, opts UploadOptions) (UploadResult, error)
	Delete(ctx context.Context, objectKey string) error
	PublicURL(objectKey string) string
	Enabled() bool
}

// Init 初始化默认文件存储适配器。
func Init(cfg config.StorageConfig) error {
	store, err := NewStore(cfg)
	if err != nil {
		return err
	}
	defaultStore = store
	return nil
}

// NewStore 根据配置创建文件存储适配器。
func NewStore(cfg config.StorageConfig) (Store, error) {
	if cfg.IsLocal() {
		return NewLocalStore(cfg.LocalDir, cfg.LocalBaseURL)
	}
	return NewS3Store(cfg)
}

// Default 返回默认文件存储适配器。
func Default() Store {
	return defaultStore
}

// Upload 使用默认适配器上传文件。
func Upload(ctx context.Context, file io.Reader, filename string, contentLength int64, opts UploadOptions) (UploadResult, error) {
	if defaultStore == nil || !defaultStore.Enabled() {
		return UploadResult{}, fmt.Errorf("文件存储未初始化")
	}
	return defaultStore.Upload(ctx, file, filename, contentLength, opts)
}

// PublicURL 返回对象公开访问地址。
func PublicURL(objectKey string) string {
	if defaultStore == nil {
		return ""
	}
	return defaultStore.PublicURL(objectKey)
}

// LocalFilePath 返回本地存储文件路径，用于开发环境文件代理。
func LocalFilePath(filename string) (string, bool) {
	store, ok := defaultStore.(*LocalStore)
	if !ok || store == nil {
		return "", false
	}
	cleanName := filepath.Base(normalizeObjectKey(filename))
	if cleanName == "." || cleanName == "" {
		return "", false
	}
	return filepath.Join(store.dir, cleanName), true
}

// LocalFilePathWithDir 返回指定本地目录下的文件路径。
func LocalFilePathWithDir(dir string, filename string) (string, bool) {
	cleanName := filepath.Base(normalizeObjectKey(filename))
	if cleanName == "." || cleanName == "" {
		return "", false
	}
	if strings.TrimSpace(dir) == "" {
		dir = "data/uploads"
	}
	return filepath.Join(dir, cleanName), true
}

// LocalStore 实现本地文件存储。
type LocalStore struct {
	dir     string
	baseURL string
}

// NewLocalStore 创建本地文件存储适配器。
func NewLocalStore(dir, baseURL string) (*LocalStore, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "data/uploads"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &LocalStore{dir: dir, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

// Upload 保存文件到本地目录。
func (s *LocalStore) Upload(_ context.Context, file io.Reader, filename string, _ int64, opts UploadOptions) (UploadResult, error) {
	objectKey := buildObjectKey(filename, opts)
	savePath := filepath.Join(s.dir, filepath.Base(objectKey))
	dst, err := os.Create(savePath)
	if err != nil {
		return UploadResult{}, err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{URL: s.PublicURL(objectKey), ObjectKey: objectKey, Storage: "local"}, nil
}

// Delete 删除本地文件。
func (s *LocalStore) Delete(_ context.Context, objectKey string) error {
	filename := filepath.Base(normalizeObjectKey(objectKey))
	if filename == "." || filename == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(s.dir, filename)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PublicURL 返回本地文件访问地址。
func (s *LocalStore) PublicURL(objectKey string) string {
	filename := filepath.Base(normalizeObjectKey(objectKey))
	if s.baseURL == "" {
		return "/uploads/" + filename
	}
	return s.baseURL + "/" + filename
}

// Enabled 返回本地文件存储是否可用。
func (s *LocalStore) Enabled() bool {
	return s != nil && s.dir != ""
}

// S3Store 实现 S3 兼容对象存储。
type S3Store struct {
	cfg    config.StorageConfig
	client *s3.Client
}

// NewS3Store 创建 S3 兼容对象存储适配器。
func NewS3Store(cfg config.StorageConfig) (*S3Store, error) {
	if strings.TrimSpace(cfg.BucketName) == "" {
		return nil, fmt.Errorf("APP_OSS_BUCKET_NAME 未配置")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.ForcePathStyle
		if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
			options.BaseEndpoint = &endpoint
		}
	})
	return &S3Store{cfg: cfg, client: client}, nil
}

// Upload 上传文件到 S3 兼容对象存储。
func (s *S3Store) Upload(ctx context.Context, file io.Reader, filename string, contentLength int64, opts UploadOptions) (UploadResult, error) {
	objectKey := buildObjectKey(filename, opts)
	input := &s3.PutObjectInput{
		Bucket: &s.cfg.BucketName,
		Key:    &objectKey,
		Body:   file,
	}
	if contentLength > 0 {
		input.ContentLength = &contentLength
	}
	if contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); contentType != "" {
		input.ContentType = &contentType
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{URL: s.PublicURL(objectKey), ObjectKey: objectKey, Storage: "oss"}, nil
}

// Delete 删除 S3 兼容对象。
func (s *S3Store) Delete(ctx context.Context, objectKey string) error {
	objectKey = normalizeObjectKey(objectKey)
	if objectKey == "" {
		return nil
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.cfg.BucketName, Key: &objectKey})
	return err
}

// PublicURL 返回对象公开访问地址。
func (s *S3Store) PublicURL(objectKey string) string {
	objectKey = normalizeObjectKey(objectKey)
	if objectKey == "" {
		return ""
	}
	if baseURL := strings.TrimRight(s.cfg.PublicBaseURL, "/"); baseURL != "" {
		return baseURL + "/" + objectKey
	}
	endpoint := strings.TrimRight(s.cfg.Endpoint, "/")
	if endpoint == "" {
		return objectKey
	}
	scheme := "https"
	if !s.cfg.UseSSL {
		scheme = "http"
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return strings.TrimRight(endpoint, "/") + "/" + s.cfg.BucketName + "/" + objectKey
	}
	return fmt.Sprintf("%s://%s/%s/%s", scheme, endpoint, s.cfg.BucketName, objectKey)
}

// Enabled 返回 S3 存储是否可用。
func (s *S3Store) Enabled() bool {
	return s != nil && s.client != nil
}

// buildObjectKey 生成稳定的对象键路径。
func buildObjectKey(filename string, opts UploadOptions) string {
	ext := strings.ToLower(filepath.Ext(filename))
	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	fileType := normalizePathSegment(opts.FileType, "general")
	refSegment := "unbound"
	if opts.RefID > 0 {
		refSegment = fmt.Sprintf("%d", opts.RefID)
	}
	if opts.TenantID > 0 {
		return fmt.Sprintf("tenants/%d/%s/%s/%s", opts.TenantID, fileType, refSegment, name)
	}
	return fmt.Sprintf("%s/%s/%s", fileType, refSegment, name)
}

// normalizeObjectKey 规范化对象键，禁止目录穿越。
func normalizeObjectKey(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	value = strings.TrimLeft(value, "/")
	value = strings.TrimPrefix(value, "uploads/")
	value = filepath.ToSlash(filepath.Clean(value))
	if value == "." || strings.HasPrefix(value, "../") || value == ".." {
		return ""
	}
	return value
}

// normalizePathSegment 规范化对象键路径片段。
func normalizePathSegment(value, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	replacer := strings.NewReplacer("\\", "_", "/", "_", ".", "_", " ", "_")
	value = replacer.Replace(value)
	chars := make([]rune, 0, len(value))
	for _, item := range value {
		if (item >= 'a' && item <= 'z') || (item >= '0' && item <= '9') || item == '_' || item == '-' {
			chars = append(chars, item)
		}
	}
	if len(chars) == 0 {
		return fallback
	}
	return string(chars)
}
