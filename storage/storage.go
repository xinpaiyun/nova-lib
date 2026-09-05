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

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/xinpaiyun/nova-lib/config"
)

var (
	defaultStore Store
	defaultCfg   config.StorageConfig
)

// 编译期保证两种存储实现满足 Store 接口。
var (
	_ Store = (*LocalStore)(nil)
	_ Store = (*S3Store)(nil)
)

// UploadOptions 描述上传对象的业务归属。
// ObjectKey 非空时显式指定对象键（用于兼容既有键布局契约），否则自动生成。
// ContentType 非空时显式指定内容类型，否则按文件扩展名推断。
type UploadOptions struct {
	TenantID    uint64
	FileType    string
	RefID       uint64
	ObjectKey   string
	ContentType string
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
	PresignGetURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error)
	ReadURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error)
	MoveFile(ctx context.Context, sourceKey, targetKey string) (string, error)
	Download(ctx context.Context, objectKey string, targetPath string) error
	Probe(ctx context.Context) error
	Enabled() bool
}

// Init 初始化默认文件存储适配器。
// 私有桶且配置了 CacheDir 时自动启用本地缓存清理器。
func Init(cfg config.StorageConfig) error {
	store, err := NewStore(cfg)
	if err != nil {
		return err
	}
	defaultStore = store
	defaultCfg = cfg
	if cfg.PrivateBucket && strings.TrimSpace(cfg.CacheDir) != "" {
		if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
			return fmt.Errorf("failed to create object cache directory: %v", err)
		}
		StartCacheCleaner(cfg.CacheDir, CachePolicyFromConfig(cfg))
	} else {
		StopCacheCleaner()
	}
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

// ReadURL 按默认存储的桶模式返回最优读取地址：
// 公有桶返回公有直连地址，私有桶返回短时预签名 URL。
func ReadURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	if defaultStore == nil || !defaultStore.Enabled() {
		return "", fmt.Errorf("文件存储未初始化")
	}
	return defaultStore.ReadURL(ctx, objectKey, ttl)
}

// Config 返回默认存储配置副本，供业务层判断桶模式与缓存目录。
func Config() config.StorageConfig {
	return defaultCfg
}

// IsPrivateBucket 返回默认存储是否为私有桶模式。
func IsPrivateBucket() bool {
	return defaultCfg.PrivateBucket
}

// LocalFilePath 返回本地存储文件路径，用于开发环境文件代理。
func LocalFilePath(objectKey string) (string, bool) {
	store, ok := defaultStore.(*LocalStore)
	if !ok || store == nil {
		return "", false
	}
	return store.localPath(objectKey)
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

// Upload 保存文件到本地目录（本地布局按文件名平铺，与对象键层级无关）。
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

// Probe 检查本地存储可用性（目录已存在即可写）。
func (s *LocalStore) Probe(_ context.Context) error {
	if !s.Enabled() {
		return fmt.Errorf("本地文件存储未初始化")
	}
	return nil
}

// Download 复制本地存储文件到目标路径。
func (s *LocalStore) Download(_ context.Context, objectKey string, targetPath string) error {
	sourcePath, ok := s.localPath(objectKey)
	if !ok {
		return fmt.Errorf("object key 不合法")
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return fmt.Errorf("failed to read file from local storage: %v", err)
	}
	return copyFileToPath(sourcePath, targetPath)
}

// PresignGetURL 返回本地存储文件的访问地址（本地模式无需签名）。
func (s *LocalStore) PresignGetURL(_ context.Context, objectKey string, _ time.Duration) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("本地文件存储未初始化")
	}
	return s.PublicURL(objectKey), nil
}

// ReadURL 返回本地文件访问地址（本地模式无公私桶差异）。
func (s *LocalStore) ReadURL(_ context.Context, objectKey string, _ time.Duration) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("本地文件存储未初始化")
	}
	return s.PublicURL(objectKey), nil
}

// MoveFile 将本地存储文件移动到新的对象键，并返回新的访问地址。
func (s *LocalStore) MoveFile(_ context.Context, sourceKey, targetKey string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("本地文件存储未初始化")
	}
	sourcePath, ok := s.localPath(sourceKey)
	if !ok {
		return "", fmt.Errorf("source object key 不合法")
	}
	targetPath, ok := s.localPath(targetKey)
	if !ok {
		return "", fmt.Errorf("target object key 不合法")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return "", err
	}
	return s.PublicURL(normalizeObjectKey(targetKey)), nil
}

// localPath 返回对象键对应的本地文件路径（平铺布局，取文件名，禁止目录穿越）。
func (s *LocalStore) localPath(objectKey string) (string, bool) {
	filename := filepath.Base(normalizeObjectKey(objectKey))
	if filename == "." || filename == "" {
		return "", false
	}
	return filepath.Join(s.dir, filename), true
}

// S3Store 实现 S3 兼容对象存储。
type S3Store struct {
	cfg    config.StorageConfig
	client *s3.Client
}

// NewS3Store 创建 S3 兼容对象存储适配器。
func NewS3Store(cfg config.StorageConfig) (*S3Store, error) {
	if strings.TrimSpace(cfg.BucketName) == "" {
		return nil, fmt.Errorf("storage.bucket_name 未配置")
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
		if endpoint := strings.TrimSpace(s3Endpoint(cfg)); endpoint != "" {
			options.BaseEndpoint = &endpoint
		}
		// 阿里云 OSS S3 兼容层不支持 SDK 默认的 aws-chunked 校验和上传。
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return &S3Store{cfg: cfg, client: client}, nil
}

// s3Endpoint 返回 SDK 使用的 S3 兼容 endpoint，内网直连地址优先。
func s3Endpoint(cfg config.StorageConfig) string {
	if endpoint := strings.TrimSpace(cfg.InternalEndpoint); endpoint != "" {
		return normalizeS3Endpoint(endpoint, cfg.UseSSL)
	}
	return normalizeS3Endpoint(cfg.Endpoint, cfg.UseSSL)
}

// normalizeS3Endpoint 将 endpoint 规范化为带协议、无尾斜杠的 URL。
func normalizeS3Endpoint(endpoint string, useSSL bool) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		scheme := "http"
		if useSSL {
			scheme = "https"
		}
		trimmed = fmt.Sprintf("%s://%s", scheme, trimmed)
	}
	return strings.TrimRight(trimmed, "/")
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
	if opts.ContentType != "" {
		input.ContentType = &opts.ContentType
	} else if contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); contentType != "" {
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

// Probe 使用 HeadBucket 校验桶配置与凭据是否可用。
func (s *S3Store) Probe(ctx context.Context) error {
	if !s.Enabled() {
		return fmt.Errorf("对象存储未初始化")
	}
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.cfg.BucketName})
	return err
}

// Download 下载对象到本地目标路径（先写临时文件再原子改名）。
func (s *S3Store) Download(ctx context.Context, objectKey string, targetPath string) error {
	key := normalizeObjectKey(objectKey)
	if key == "" {
		return fmt.Errorf("object key 不能为空")
	}
	if !s.Enabled() {
		return fmt.Errorf("对象存储未初始化")
	}
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.BucketName, Key: &key})
	if err != nil {
		return fmt.Errorf("failed to read file from object storage: %v", err)
	}
	defer resp.Body.Close()

	tmpPath := fmt.Sprintf("%s.tmp.%d", targetPath, time.Now().UnixNano())
	dst, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create object cache file: %v", err)
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write object cache file: %v", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close object cache file: %v", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to move object cache file: %v", err)
	}
	return nil
}

// ReadURL 按桶模式返回最优读取地址：
// 公有桶返回公有直连地址；私有桶返回短时预签名 URL（外部服务端读取私有对象）。
func (s *S3Store) ReadURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	if !s.cfg.PrivateBucket {
		if !s.Enabled() {
			return "", fmt.Errorf("对象存储未初始化")
		}
		return s.PublicURL(objectKey), nil
	}
	return s.PresignGetURL(ctx, objectKey, ttl)
}

// PresignGetURL 生成对象临时访问地址。
func (s *S3Store) PresignGetURL(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	key := normalizeObjectKey(objectKey)
	if key == "" {
		return "", fmt.Errorf("object key 不能为空")
	}
	if !s.Enabled() {
		return "", fmt.Errorf("对象存储未初始化")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	out, err := s3.NewPresignClient(s.client).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.cfg.BucketName,
		Key:    &key,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

// MoveFile 将已有对象复制到新的对象键并删除源对象，返回新的访问地址。
func (s *S3Store) MoveFile(ctx context.Context, sourceKey, targetKey string) (string, error) {
	source := normalizeObjectKey(sourceKey)
	target := normalizeObjectKey(targetKey)
	if source == "" || target == "" {
		return "", fmt.Errorf("source 与 target object key 均不能为空")
	}
	if !s.Enabled() {
		return "", fmt.Errorf("对象存储未初始化")
	}
	if source == target {
		return s.PublicURL(target), nil
	}
	copySource := strings.ReplaceAll(url.PathEscape(s.cfg.BucketName+"/"+source), "%2F", "/")
	if _, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &s.cfg.BucketName,
		Key:        &target,
		CopySource: &copySource,
	}); err != nil {
		return "", err
	}
	if err := s.Delete(ctx, source); err != nil {
		return "", err
	}
	return s.PublicURL(target), nil
}

// buildObjectKey 生成稳定的对象键路径；显式指定 ObjectKey 时直接使用。
func buildObjectKey(filename string, opts UploadOptions) string {
	if explicit := normalizeObjectKey(opts.ObjectKey); explicit != "" {
		return explicit
	}
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

// normalizeObjectKey 规范化对象键，禁止目录穿越，并剥离后端文件代理前缀。
func normalizeObjectKey(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	value = strings.TrimLeft(value, "/")
	value = strings.TrimPrefix(value, "uploads/")
	value = strings.TrimPrefix(value, "v1/files/")
	value = strings.TrimPrefix(value, "files/")
	value = filepath.ToSlash(filepath.Clean(value))
	if value == "." || strings.HasPrefix(value, "../") || value == ".." {
		return ""
	}
	return value
}

// NormalizeObjectKey 规范化对象键：剥离代理前缀、禁止目录穿越。
func NormalizeObjectKey(value string) string {
	return normalizeObjectKey(value)
}

// copyFileToPath 将本地文件复制到目标路径（先写临时文件再原子改名）。
func copyFileToPath(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to read file from local storage: %v", err)
	}
	defer source.Close()

	tmpPath := fmt.Sprintf("%s.tmp.%d", targetPath, time.Now().UnixNano())
	dst, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create local cache file: %v", err)
	}
	if _, err := io.Copy(dst, source); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write local cache file: %v", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close local cache file: %v", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to move local cache file: %v", err)
	}
	return nil
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
