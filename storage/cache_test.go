package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestCleanupCacheOnceRemovesExpiredFiles 验证过期缓存文件会被清理。
func TestCleanupCacheOnceRemovesExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "active.bin")
	expired := filepath.Join(dir, "nested", "expired.bin")
	if err := os.MkdirAll(filepath.Dir(expired), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("active"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, []byte("expired"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(expired, stale, stale); err != nil {
		t.Fatal(err)
	}

	policy := CachePolicy{TTL: 7 * 24 * time.Hour}
	if err := CleanupCacheOnce(dir, policy, time.Now()); err != nil {
		t.Fatalf("CleanupCacheOnce() error = %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active cache file should be kept: %v", err)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatalf("expired cache file should be removed")
	}
	// 空父目录应被一并清理
	if _, err := os.Stat(filepath.Dir(expired)); !os.IsNotExist(err) {
		t.Fatalf("empty parent dir should be removed")
	}
}

// TestCleanupCacheOnceKeepsActiveTempFiles 验证未过期的临时文件不会被误删。
func TestCleanupCacheOnceKeepsActiveTempFiles(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "object.tmp.123")
	if err := os.WriteFile(temp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CleanupCacheOnce(dir, CachePolicy{TTL: 7 * 24 * time.Hour}, time.Now()); err != nil {
		t.Fatalf("CleanupCacheOnce() error = %v", err)
	}
	if _, err := os.Stat(temp); err != nil {
		t.Fatalf("active temp file should be kept: %v", err)
	}
}

// TestCleanupCacheOnceTrimsOldestFiles 验证超出容量上限时优先清理最旧文件。
func TestCleanupCacheOnceTrimsOldestFiles(t *testing.T) {
	dir := t.TempDir()
	oldest := filepath.Join(dir, "oldest.bin")
	newest := filepath.Join(dir, "newest.bin")
	if err := os.WriteFile(oldest, make([]byte, 600), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newest, make([]byte, 600), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldest, stale, stale); err != nil {
		t.Fatal(err)
	}

	policy := CachePolicy{MaxBytes: 1024}
	if err := CleanupCacheOnce(dir, policy, time.Now()); err != nil {
		t.Fatalf("CleanupCacheOnce() error = %v", err)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("oldest file should be removed first")
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("newest file should be kept: %v", err)
	}
}

// TestBuildObjectKeyWithExplicitKey 验证显式指定对象键时跳过自动生成。
func TestBuildObjectKeyWithExplicitKey(t *testing.T) {
	got := buildObjectKey("a.png", UploadOptions{ObjectKey: "teams/3/report/1/x.png"})
	if got != "teams/3/report/1/x.png" {
		t.Fatalf("buildObjectKey() = %q, want explicit key", got)
	}
	if got := buildObjectKey("a.png", UploadOptions{ObjectKey: "/v1/files/teams/3/x.png"}); got != "teams/3/x.png" {
		t.Fatalf("buildObjectKey(proxy url) = %q, want stripped key", got)
	}
}

// TestNormalizeObjectKeyStripsProxyPrefix 验证后端文件代理前缀剥离与穿越防护。
func TestNormalizeObjectKeyStripsProxyPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "/v1/files/teams/1/x.png", want: "teams/1/x.png"},
		{in: "files/teams/1/x.png", want: "teams/1/x.png"},
		{in: "uploads/x.png", want: "x.png"},
		{in: "../etc/passwd", want: ""},
		{in: "  ", want: ""},
	}
	for _, tt := range tests {
		if got := normalizeObjectKey(tt.in); got != tt.want {
			t.Fatalf("normalizeObjectKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestLocalStoreDownload 验证本地存储的 Download 复制能力。
func TestLocalStoreDownload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "http://localhost:8080/uploads")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upload(context.Background(), strings.NewReader("hello"), "a.txt", 5, UploadOptions{FileType: "report"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 uploaded file, got %d", len(entries))
	}
	target := filepath.Join(t.TempDir(), "copy.txt")
	if err := store.Download(context.Background(), entries[0].Name(), target); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "hello" {
		t.Fatalf("Download() content = %q, err = %v", data, err)
	}
}

// TestCachePolicyFromConfig 验证缓存策略默认值与配置覆盖。
func TestCachePolicyFromConfig(t *testing.T) {
	def := CachePolicyFromConfig(config.StorageConfig{})
	if def.TTL != DefaultCacheTTLHours*time.Hour || def.MaxBytes != DefaultCacheMaxSizeMB*1024*1024 {
		t.Fatalf("default policy = %+v", def)
	}
	custom := CachePolicyFromConfig(config.StorageConfig{CacheTTLHours: 24, CacheMaxSizeMB: 512})
	if custom.TTL != 24*time.Hour || custom.MaxBytes != 512*1024*1024 {
		t.Fatalf("custom policy = %+v", custom)
	}
}
