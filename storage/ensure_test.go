package storage

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// fakeOSS 模拟 S3 兼容对象存储的桶级接口，记录调用轨迹。
type fakeOSS struct {
	mu          sync.Mutex
	headStatus  int
	createCalls int
	createErr   int
	aclCalls    []string
	logger      *testing.T
}

func (f *fakeOSS) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.logger != nil {
		f.logger.Logf("%s %s amz-acl=%q", r.Method, r.URL.String(), r.Header.Get("x-amz-acl"))
	}
	switch r.Method {
	case http.MethodHead:
		w.WriteHeader(f.headStatus)
	case http.MethodPut:
		if r.URL.Query().Has("acl") || r.Header.Get("x-amz-acl") != "" {
			f.aclCalls = append(f.aclCalls, r.Header.Get("x-amz-acl"))
			w.WriteHeader(http.StatusOK)
			return
		}
		f.createCalls++
		if f.createErr != 0 {
			w.WriteHeader(f.createErr)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (f *fakeOSS) newStore(t *testing.T, privateBucket bool) *S3Store {
	t.Helper()
	f.mu.Lock()
	f.logger = t
	f.mu.Unlock()
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(server.Close)
	store, err := NewS3Store(config.StorageConfig{
		Driver:          "s3",
		Endpoint:        server.Listener.Addr().String(),
		AccessKeyID:     "test",
		AccessKeySecret: "test",
		BucketName:      "app-bucket",
		Region:          "cn-hangzhou",
		UseSSL:          false,
		ForcePathStyle:  true,
		PrivateBucket:   privateBucket,
	})
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}
	return store
}

// TestEnsureBucketCreatesPublicRead 公共桶缺失时自动建桶并设置 public-read。
func TestEnsureBucketCreatesPublicRead(t *testing.T) {
	fake := &fakeOSS{headStatus: http.StatusNotFound}
	fake.newStore(t, false)
	if fake.createCalls != 1 {
		t.Fatalf("CreateBucket calls = %d, want 1", fake.createCalls)
	}
	if len(fake.aclCalls) != 1 || fake.aclCalls[0] != "public-read" {
		t.Fatalf("ACL calls = %v, want [public-read]", fake.aclCalls)
	}
}

// TestEnsureBucketSkipsACLForPrivate 私有桶建桶后不改 ACL。
func TestEnsureBucketSkipsACLForPrivate(t *testing.T) {
	fake := &fakeOSS{headStatus: http.StatusNotFound}
	fake.newStore(t, true)
	if fake.createCalls != 1 || len(fake.aclCalls) != 0 {
		t.Fatalf("create = %d, acl = %d, want 1 create and no ACL", fake.createCalls, len(fake.aclCalls))
	}
}

// TestEnsureBucketSkipsExisting 桶已存在时不创建也不改 ACL。
func TestEnsureBucketSkipsExisting(t *testing.T) {
	fake := &fakeOSS{headStatus: http.StatusOK}
	fake.newStore(t, false)
	if fake.createCalls != 0 || len(fake.aclCalls) != 0 {
		t.Fatalf("create = %d, acl = %d, want 0", fake.createCalls, len(fake.aclCalls))
	}
}

// TestEnsureBucketSkipsUndetermined 探测因网络/凭据等无法判定时静默跳过。
func TestEnsureBucketSkipsUndetermined(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		fake := &fakeOSS{headStatus: status}
		fake.newStore(t, false)
		if fake.createCalls != 0 {
			t.Fatalf("head %d: create = %d, want 0", status, fake.createCalls)
		}
	}
}

// TestEnsureBucketAlreadyExistsConcurrent 创建撞上并发已存在时视为成功。
func TestEnsureBucketAlreadyExistsConcurrent(t *testing.T) {
	fake := &fakeOSS{headStatus: http.StatusNotFound, createErr: http.StatusConflict}
	fake.newStore(t, false)
}

// TestShouldCreateBucketDecisionTable 覆盖探测错误的决策表。
func TestShouldCreateBucketDecisionTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"transport error", &fakeHTTPError{status: 0}, false},
		{"not found", &fakeHTTPError{status: 404}, true},
		{"forbidden", &fakeHTTPError{status: 403}, true},
		{"server error", &fakeHTTPError{status: 503}, false},
	}
	for _, tc := range cases {
		if got := shouldCreateBucket(tc.err); got != tc.want {
			t.Fatalf("%s: shouldCreateBucket = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// fakeHTTPError 模拟带 HTTP 状态码的 S3 错误。
type fakeHTTPError struct{ status int }

func (e *fakeHTTPError) Error() string       { return "fake s3 error" }
func (e *fakeHTTPError) HTTPStatusCode() int { return e.status }
