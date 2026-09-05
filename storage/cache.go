package storage

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/logging"
)

const (
	// DefaultCacheTTLHours 本地对象缓存默认过期时间（7 天）。
	DefaultCacheTTLHours = 168
	// DefaultCacheMaxSizeMB 本地对象缓存默认容量上限（2 GiB）。
	DefaultCacheMaxSizeMB = 2048

	cacheCleanupInterval = time.Hour
	cacheTempStaleAge    = time.Hour
)

var (
	cacheMu       sync.Mutex
	cacheStopChan chan struct{}
	cacheRunning  bool
)

// CachePolicy 描述本地对象缓存的清理策略。
type CachePolicy struct {
	TTL      time.Duration
	MaxBytes int64
}

// CachePolicyFromConfig 根据存储配置生成本地缓存清理策略。
func CachePolicyFromConfig(cfg config.StorageConfig) CachePolicy {
	ttlHours := DefaultCacheTTLHours
	maxSizeMB := int64(DefaultCacheMaxSizeMB)
	if cfg.CacheTTLHours != 0 {
		ttlHours = cfg.CacheTTLHours
	}
	if cfg.CacheMaxSizeMB != 0 {
		maxSizeMB = cfg.CacheMaxSizeMB
	}
	return CachePolicy{
		TTL:      time.Duration(ttlHours) * time.Hour,
		MaxBytes: maxSizeMB * 1024 * 1024,
	}
}

// CachedObject 使用默认存储与配置的 CacheDir 缓存对象，返回本地路径和内容类型。
// 私有桶且 CacheDir 未配置时返回错误。
func CachedObject(objectKey string) (string, string, error) {
	if defaultStore == nil || !defaultStore.Enabled() {
		return "", "", fmt.Errorf("文件存储未初始化")
	}
	return CachedFile(defaultStore, defaultCfg.CacheDir, objectKey)
}

// CachedFile 将对象存储文件下载到本地缓存目录，返回可直接读取的本地路径和内容类型。
// 命中缓存时刷新文件修改时间以近似记录最近访问时间。
func CachedFile(store Store, cacheDir string, objectKey string) (string, string, error) {
	key := normalizeObjectKey(objectKey)
	if key == "" {
		return "", "", fmt.Errorf("file key is required")
	}
	if !store.Enabled() {
		return "", "", fmt.Errorf("file storage is disabled")
	}
	if local, ok := store.(*LocalStore); ok {
		if path, found := local.localPath(key); found {
			if _, err := os.Stat(path); err == nil {
				return path, ContentTypeByName(key), nil
			}
		}
		return "", "", fmt.Errorf("local file not found: %s", key)
	}
	if strings.TrimSpace(cacheDir) == "" {
		return "", "", fmt.Errorf("object cache directory is not initialized")
	}
	path := filepath.Join(cacheDir, filepath.FromSlash(key))
	if _, err := os.Stat(path); err == nil {
		touchCacheFile(path)
		return path, ContentTypeByName(key), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create object cache directory: %v", err)
	}
	if err := store.Download(context.Background(), key, path); err != nil {
		return "", "", err
	}
	return path, ContentTypeByName(key), nil
}

// ContentTypeByName 根据文件名推断响应内容类型。
func ContentTypeByName(filename string) string {
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

// StartCacheCleaner 启动本地对象缓存定时清理器（每小时执行一次）。
func StartCacheCleaner(cacheDir string, policy CachePolicy) {
	StopCacheCleaner()
	if strings.TrimSpace(cacheDir) == "" || (policy.TTL <= 0 && policy.MaxBytes <= 0) {
		return
	}
	stopChan := make(chan struct{})
	go func() {
		ticker := time.NewTicker(cacheCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				if err := CleanupCacheOnce(cacheDir, policy, time.Now()); err != nil {
					logging.Warn("cleanup object cache failed", "error", err)
				}
			}
		}
	}()
	cacheMu.Lock()
	cacheStopChan = stopChan
	cacheRunning = true
	cacheMu.Unlock()
}

// StopCacheCleaner 停止正在运行的本地对象缓存清理器。
func StopCacheCleaner() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheStopChan != nil {
		close(cacheStopChan)
		cacheStopChan = nil
	}
	cacheRunning = false
}

// CleanupCacheOnce 按过期时间和容量上限执行一次本地缓存清理。
func CleanupCacheOnce(dir string, policy CachePolicy, now time.Time) error {
	var files []cacheFileEntry
	var totalSize int64
	var removedCount int
	var freedBytes int64
	expireBefore := now.Add(-policy.TTL)

	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if strings.Contains(filepath.Base(path), ".tmp.") {
			if info.ModTime().Before(now.Add(-cacheTempStaleAge)) && removeCacheFile(dir, path) {
				removedCount++
				freedBytes += info.Size()
			}
			return nil
		}
		if policy.TTL > 0 && info.ModTime().Before(expireBefore) {
			if removeCacheFile(dir, path) {
				removedCount++
				freedBytes += info.Size()
			}
			return nil
		}
		totalSize += info.Size()
		files = append(files, cacheFileEntry{path: path, size: info.Size(), modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return err
	}

	if policy.MaxBytes > 0 && totalSize > policy.MaxBytes {
		sort.Slice(files, func(i, j int) bool {
			return files[i].modTime.Before(files[j].modTime)
		})
		for _, file := range files {
			if totalSize <= policy.MaxBytes {
				break
			}
			if removeCacheFile(dir, file.path) {
				totalSize -= file.size
				removedCount++
				freedBytes += file.size
			}
		}
	}

	if removedCount > 0 {
		logging.Info("object cache cleanup finished", "removed", removedCount, "freed_bytes", freedBytes)
	}
	return nil
}

type cacheFileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

// removeCacheFile 删除缓存文件并清理空父目录。
func removeCacheFile(rootDir, path string) bool {
	if err := os.Remove(path); err != nil {
		return false
	}
	removeEmptyCacheDirs(rootDir, filepath.Dir(path))
	return true
}

// removeEmptyCacheDirs 自底向上删除缓存目录中的空目录。
func removeEmptyCacheDirs(rootDir, dir string) {
	rootDir = filepath.Clean(rootDir)
	for {
		cleanDir := filepath.Clean(dir)
		if cleanDir == rootDir || cleanDir == "." || cleanDir == string(filepath.Separator) {
			return
		}
		if err := os.Remove(cleanDir); err != nil {
			return
		}
		dir = filepath.Dir(cleanDir)
	}
}

// touchCacheFile 刷新缓存文件的修改时间，用于近似记录最近访问时间。
func touchCacheFile(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}
