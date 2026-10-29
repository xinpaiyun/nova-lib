// Package address 提供面向业务模块的通用地址能力：经纬度逆地理解析与关键词地点搜索，
// 统一收敛服务未配置、参数校验与错误文案，底层由 tencentmap 客户端实现。
package address

import (
	"context"
	"errors"
	"strings"

	"github.com/xinpaiyun/nova-lib/tencentmap"
)

// Result 定义通用的逆地理解析结果。
type Result = tencentmap.Result

// Place 定义地点搜索候选结果。
type Place = tencentmap.Place

// Geocoder 逆地理解析能力（由 *tencentmap.Client 实现）。
type Geocoder interface {
	IsConfigured() bool
	ReverseGeocode(ctx context.Context, latitude, longitude float64) (*Result, error)
}

// Suggester 地点搜索能力（由 *tencentmap.Client 实现）。
type Suggester interface {
	IsConfigured() bool
	Suggestion(ctx context.Context, keyword, region string) ([]Place, error)
}

// Service 面向业务模块的通用地址服务。
type Service struct {
	geocoder  Geocoder
	suggester Suggester
}

// New 创建地址服务；geocoder/suggester 可为 nil，对应能力调用时返回未配置错误。
// 两者通常传入同一个 *tencentmap.Client。
func New(geocoder Geocoder, suggester Suggester) *Service {
	return &Service{geocoder: geocoder, suggester: suggester}
}

// ReverseGeocode 根据经纬度解析标准省市区信息。
func (s *Service) ReverseGeocode(ctx context.Context, latitude, longitude float64) (*Result, error) {
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, errors.New("经纬度参数不合法")
	}
	if s == nil || s.geocoder == nil || !s.geocoder.IsConfigured() {
		return nil, errors.New("地址解析服务未配置")
	}
	return s.geocoder.ReverseGeocode(ctx, latitude, longitude)
}

// Suggest 关键词搜索候选地点（region 为检索范围，如城市名，空则全国）。
func (s *Service) Suggest(ctx context.Context, keyword, region string) ([]Place, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, errors.New("请输入搜索关键词")
	}
	if s == nil || s.suggester == nil || !s.suggester.IsConfigured() {
		return nil, errors.New("地点搜索服务未配置")
	}
	return s.suggester.Suggestion(ctx, keyword, region)
}
