package tencentmap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestBuildSignature 验证腾讯位置服务 sig 计算规则（path?排序参数+SK 的 MD5）。
func TestBuildSignature(t *testing.T) {
	params := map[string]string{"key": "K", "location": "39.9,116.4"}
	got := buildSignature("/ws/geocoder/v1", params, "SECRET")
	// payload = "/ws/geocoder/v1?key=K&location=39.9,116.4SECRET"
	want := "1fded50418b607a617d576af56a1770c"
	if got != want {
		t.Fatalf("buildSignature() = %q, want %q", got, want)
	}
}

// TestFormatLocation 验证经纬度格式化精度。
func TestFormatLocation(t *testing.T) {
	if got := formatLocation(39.90469, 116.40717); got != "39.904690,116.407170" {
		t.Fatalf("formatLocation() = %q", got)
	}
}

// TestSuggestion 验证地点搜索的参数拼装、签名透传与结果映射。
func TestSuggestion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != suggestionPath {
			t.Errorf("path = %q, want %q", r.URL.Path, suggestionPath)
		}
		if got := r.URL.Query().Get("keyword"); got != "故宫" {
			t.Errorf("keyword = %q, want 故宫", got)
		}
		if got := r.URL.Query().Get("region"); got != "北京" {
			t.Errorf("region = %q, want 北京", got)
		}
		if r.URL.Query().Get("key") == "" || r.URL.Query().Get("sig") == "" {
			t.Errorf("missing key/sig: %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"status":0,"data":[{"id":"1","title":"故宫博物院","address":"景山前街4号","province":"北京市","city":"北京市","district":"东城区","location":{"lat":39.91805,"lng":116.397}}]}`))
	}))
	defer srv.Close()

	client := NewClient(config.TencentMapConfig{WebServiceKey: "K", WebServiceSK: "S"})
	client.baseURL = srv.URL
	places, err := client.Suggestion(context.Background(), "  故宫  ", "北京")
	if err != nil {
		t.Fatalf("Suggestion() error = %v", err)
	}
	if len(places) != 1 {
		t.Fatalf("len(places) = %d, want 1", len(places))
	}
	p := places[0]
	if p.ID != "1" || p.Title != "故宫博物院" || p.Address != "景山前街4号" || p.Province != "北京市" || p.City != "北京市" || p.District != "东城区" {
		t.Fatalf("place = %+v", p)
	}
	if p.Latitude != 39.91805 || p.Longitude != 116.397 {
		t.Fatalf("coord = %v,%v", p.Latitude, p.Longitude)
	}
}

// TestSuggestionErrors 验证未配置、空关键词与接口报错时的错误语义。
func TestSuggestionErrors(t *testing.T) {
	unconfigured := NewClient(config.TencentMapConfig{})
	if _, err := unconfigured.Suggestion(context.Background(), "故宫", ""); err == nil || err.Error() != "腾讯位置服务未配置" {
		t.Fatalf("unconfigured error = %v, want 腾讯位置服务未配置", err)
	}

	configured := NewClient(config.TencentMapConfig{WebServiceKey: "K", WebServiceSK: "S"})
	if _, err := configured.Suggestion(context.Background(), "  ", ""); err == nil || err.Error() != "keyword 不能为空" {
		t.Fatalf("empty keyword error = %v, want keyword 不能为空", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":120,"message":"请求来源未被授权"}`))
	}))
	defer srv.Close()
	configured.baseURL = srv.URL
	if _, err := configured.Suggestion(context.Background(), "故宫", ""); err == nil || !strings.Contains(err.Error(), "请求来源未被授权") {
		t.Fatalf("api error = %v, want 包含 请求来源未被授权", err)
	}
}
