package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
)

// newTestS3Store 创建用于本地签名与 URL 拼接验证的 S3Store（不发真实网络请求）。
func newTestS3Store(t *testing.T, privateBucket bool) *S3Store {
	t.Helper()
	store, err := NewS3Store(config.StorageConfig{
		Endpoint:        "oss-cn-hangzhou.internal.example.com",
		AccessKeyID:     "test-key",
		AccessKeySecret: "test-secret",
		BucketName:      "test-bucket",
		Region:          "cn-hangzhou",
		ForcePathStyle:  true,
		PrivateBucket:   privateBucket,
	})
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}
	return store
}

// TestReadURLEnabledPublicBucket 验证公有桶读取返回公有直连地址。
func TestReadURLEnabledPublicBucket(t *testing.T) {
	store := newTestS3Store(t, false)
	url, err := store.ReadURL(context.Background(), "teams/1/x.png", time.Minute)
	if err != nil {
		t.Fatalf("ReadURL() error = %v", err)
	}
	if url != "http://oss-cn-hangzhou.internal.example.com/test-bucket/teams/1/x.png" {
		t.Fatalf("ReadURL() = %q, want public direct url", url)
	}
	if strings.Contains(url, "X-Amz-Signature") {
		t.Fatalf("public bucket url should not be signed: %q", url)
	}
}

// TestReadURLPrivateBucketPresigns 验证私有桶读取返回预签名地址。
func TestReadURLPrivateBucketPresigns(t *testing.T) {
	store := newTestS3Store(t, true)
	url, err := store.ReadURL(context.Background(), "teams/1/x.png", 10*time.Minute)
	if err != nil {
		t.Fatalf("ReadURL() error = %v", err)
	}
	if !strings.Contains(url, "X-Amz-Signature") {
		t.Fatalf("private bucket url should be presigned: %q", url)
	}
	if !strings.Contains(url, "test-bucket/teams/1/x.png") {
		t.Fatalf("presigned url should contain object key: %q", url)
	}
}

// TestLocalStoreReadURL 验证本地存储读取地址。
func TestLocalStoreReadURL(t *testing.T) {
	store, err := NewLocalStore(t.TempDir(), "http://localhost:8080/uploads")
	if err != nil {
		t.Fatal(err)
	}
	url, err := store.ReadURL(context.Background(), "x.png", time.Minute)
	if err != nil {
		t.Fatalf("ReadURL() error = %v", err)
	}
	if url != "http://localhost:8080/uploads/x.png" {
		t.Fatalf("ReadURL() = %q", url)
	}
}

// TestPublicURLKeepsDirectSemantics 验证 PublicURL 在私有桶下仍返回直连拼装地址（内部服务自用）。
func TestPublicURLKeepsDirectSemantics(t *testing.T) {
	store := newTestS3Store(t, true)
	if got := store.PublicURL("teams/1/x.png"); !strings.Contains(got, "test-bucket/teams/1/x.png") {
		t.Fatalf("PublicURL() = %q", got)
	}
}
