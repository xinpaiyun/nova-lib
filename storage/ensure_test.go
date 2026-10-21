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

// TestOssLocationConstraint 阿里云 OSS 端点补 oss- 区域前缀，其余端点原样返回。
func TestOssLocationConstraint(t *testing.T) {
	cases := []struct {
		name     string
		region   string
		endpoint string
		want     string
	}{
		{"aliyun plain region", "cn-beijing", "oss-cn-beijing.aliyuncs.com", "oss-cn-beijing"},
		{"aliyun internal endpoint", "cn-hangzhou", "oss-cn-hangzhou-internal.aliyuncs.com", "oss-cn-hangzhou"},
		{"aliyun already prefixed", "oss-cn-beijing", "oss-cn-beijing.aliyuncs.com", "oss-cn-beijing"},
		{"non-aliyun unchanged", "cn-beijing", "127.0.0.1:9000", "cn-beijing"},
		{"minio unchanged", "us-east-1", "minio.example.com", "us-east-1"},
	}
	for _, tc := range cases {
		if got := ossLocationConstraint(tc.region, tc.endpoint); got != tc.want {
			t.Fatalf("%s: ossLocationConstraint = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestResolveUsePathStyle 阿里云端点强制纠偏为 virtual-host，其余端点遵循配置。
func TestResolveUsePathStyle(t *testing.T) {
	cases := []struct {
		name           string
		endpoint       string
		forcePathStyle bool
		wantPathStyle  bool
		wantCorrected  bool
	}{
		{"aliyun with path style", "https://oss-cn-beijing.aliyuncs.com", true, false, true},
		{"aliyun internal with path style", "http://oss-cn-hangzhou-internal.aliyuncs.com", true, false, true},
		{"aliyun without path style", "https://oss-cn-beijing.aliyuncs.com", false, false, false},
		{"minio keeps path style", "http://127.0.0.1:9000", true, true, false},
		{"empty endpoint keeps config", "", true, true, false},
	}
	for _, tc := range cases {
		cfg := config.StorageConfig{Endpoint: tc.endpoint, UseSSL: false, ForcePathStyle: tc.forcePathStyle}
		gotPathStyle, gotCorrected := resolveUsePathStyle(cfg)
		if gotPathStyle != tc.wantPathStyle || gotCorrected != tc.wantCorrected {
			t.Fatalf("%s: resolveUsePathStyle = (%v, %v), want (%v, %v)",
				tc.name, gotPathStyle, gotCorrected, tc.wantPathStyle, tc.wantCorrected)
		}
	}
}

// TestNewS3StoreKeepsPathStyleForNonAliyun 本地兼容存储不受纠偏影响，path-style 保持生效。
func TestNewS3StoreKeepsPathStyleForNonAliyun(t *testing.T) {
	fake := &fakeOSS{headStatus: http.StatusOK}
	store := fake.newStore(t, false)
	if !store.client.Options().UsePathStyle {
		t.Fatalf("UsePathStyle = false, want true for non-aliyun endpoint")
	}
}

// fakeHTTPError 模拟带 HTTP 状态码的 S3 错误。
type fakeHTTPError struct{ status int }

func (e *fakeHTTPError) Error() string       { return "fake s3 error" }
func (e *fakeHTTPError) HTTPStatusCode() int { return e.status }
