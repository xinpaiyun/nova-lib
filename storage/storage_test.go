package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLocalStorePresignAndMove 验证本地存储的签名地址回退与文件移动行为。
func TestLocalStorePresignAndMove(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "http://localhost:8080/uploads")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	ctx := context.Background()

	result, err := store.Upload(ctx, strings.NewReader("hello"), "a.txt", 5, UploadOptions{TenantID: 1, FileType: "image"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !strings.HasPrefix(result.URL, "http://localhost:8080/uploads/") {
		t.Fatalf("URL = %q", result.URL)
	}
	if !strings.HasPrefix(result.ObjectKey, "tenants/1/image/") {
		t.Fatalf("ObjectKey = %q", result.ObjectKey)
	}
	// 本地平铺布局：文件应直接位于 dir 下。
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(result.ObjectKey))); err != nil {
		t.Fatalf("uploaded file not flattened in dir: %v", err)
	}

	url, err := store.PresignGetURL(ctx, result.ObjectKey, time.Minute)
	if err != nil {
		t.Fatalf("PresignGetURL: %v", err)
	}
	if url != result.URL {
		t.Fatalf("PresignGetURL = %q, want %q", url, result.URL)
	}

	newURL, err := store.MoveFile(ctx, result.ObjectKey, "tenants/1/image/b.txt")
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if !strings.HasSuffix(newURL, "/b.txt") {
		t.Fatalf("MoveFile URL = %q", newURL)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}

	if err := store.Delete(ctx, "tenants/1/image/b.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should be deleted")
	}
}

// TestLocalStoreMoveFileRejectsTraversal 验证对象键目录穿越被拒绝。
func TestLocalStoreMoveFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if _, err := os.Create(filepath.Join(dir, "victim.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveFile(context.Background(), "../../../etc/passwd", "x.txt"); err == nil {
		t.Fatal("expected traversal source rejected or harmless, got nil error moving")
	}
}
