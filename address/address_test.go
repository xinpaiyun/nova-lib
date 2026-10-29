package address

import (
	"context"
	"testing"

	"github.com/xinpaiyun/nova-lib/tencentmap"
)

type stubGeocoder struct {
	configured bool
	result     *tencentmap.Result
	err        error
}

func (s *stubGeocoder) IsConfigured() bool { return s.configured }

func (s *stubGeocoder) ReverseGeocode(ctx context.Context, latitude, longitude float64) (*tencentmap.Result, error) {
	return s.result, s.err
}

type stubSuggester struct {
	configured bool
	places     []tencentmap.Place
	err        error
}

func (s *stubSuggester) IsConfigured() bool { return s.configured }

func (s *stubSuggester) Suggestion(ctx context.Context, keyword, region string) ([]tencentmap.Place, error) {
	return s.places, s.err
}

// TestServiceReverseGeocode 验证参数校验、未配置语义与结果透传。
func TestServiceReverseGeocode(t *testing.T) {
	var nilService *Service
	if _, err := nilService.ReverseGeocode(context.Background(), 39.9, 116.4); err == nil || err.Error() != "地址解析服务未配置" {
		t.Fatalf("nil service error = %v, want 地址解析服务未配置", err)
	}

	svc := New(nil, nil)
	if _, err := svc.ReverseGeocode(context.Background(), 39.9, 116.4); err == nil || err.Error() != "地址解析服务未配置" {
		t.Fatalf("unconfigured error = %v, want 地址解析服务未配置", err)
	}
	if _, err := svc.ReverseGeocode(context.Background(), 91, 116.4); err == nil || err.Error() != "经纬度参数不合法" {
		t.Fatalf("invalid coord error = %v, want 经纬度参数不合法", err)
	}

	want := &tencentmap.Result{Province: "北京市", City: "北京市", District: "东城区"}
	svc = New(&stubGeocoder{configured: true, result: want}, nil)
	got, err := svc.ReverseGeocode(context.Background(), 39.9, 116.4)
	if err != nil || got != want {
		t.Fatalf("ReverseGeocode() = %+v, %v", got, err)
	}
}

// TestServiceSuggest 验证关键词校验、未配置语义与结果透传。
func TestServiceSuggest(t *testing.T) {
	var nilService *Service
	if _, err := nilService.Suggest(context.Background(), " ", ""); err == nil || err.Error() != "请输入搜索关键词" {
		t.Fatalf("nil service error = %v, want 请输入搜索关键词", err)
	}
	if _, err := nilService.Suggest(context.Background(), "故宫", ""); err == nil || err.Error() != "地点搜索服务未配置" {
		t.Fatalf("nil service error = %v, want 地点搜索服务未配置", err)
	}

	svc := New(nil, nil)
	if _, err := svc.Suggest(context.Background(), "故宫", ""); err == nil || err.Error() != "地点搜索服务未配置" {
		t.Fatalf("unconfigured error = %v, want 地点搜索服务未配置", err)
	}
	if _, err := svc.Suggest(context.Background(), "  ", ""); err == nil || err.Error() != "请输入搜索关键词" {
		t.Fatalf("empty keyword error = %v, want 请输入搜索关键词", err)
	}

	places := []tencentmap.Place{{ID: "1", Title: "故宫博物院"}}
	svc = New(&stubGeocoder{configured: true}, &stubSuggester{configured: true, places: places})
	got, err := svc.Suggest(context.Background(), " 故宫 ", "北京")
	if err != nil || len(got) != 1 || got[0].Title != "故宫博物院" {
		t.Fatalf("Suggest() = %+v, %v", got, err)
	}
}
