// Package tencentmap 提供腾讯位置服务 WebService API 的标准封装。
package tencentmap

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/cache"
	"github.com/xinpaiyun/nova-lib/config"
)

const (
	webServiceBaseURL      = "https://apis.map.qq.com"
	reverseGeocodePath     = "/ws/geocoder/v1"
	reverseGeocodeCacheTTL = 24 * time.Hour
)

// Result 定义通用的逆地理解析结果。
type Result struct {
	Province         string `json:"province"`
	City             string `json:"city"`
	District         string `json:"district"`
	Address          string `json:"address"`
	FormattedAddress string `json:"formatted_address"`
	AdCode           string `json:"adcode"`
}

// Client 封装腾讯位置服务 WebService API 调用。
type Client struct {
	cfg        config.TencentMapConfig
	httpClient *http.Client
}

type reverseGeocodeResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Result  struct {
		Address string `json:"address"`
		AdInfo  struct {
			AdCode   string `json:"adcode"`
			Province string `json:"province"`
			City     string `json:"city"`
			District string `json:"district"`
		} `json:"ad_info"`
	} `json:"result"`
}

// NewClient 创建腾讯位置服务客户端。
func NewClient(cfg config.TencentMapConfig) *Client {
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// IsConfigured 判断当前客户端是否具备调用 WebService API 的必要配置。
func (c *Client) IsConfigured() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.cfg.WebServiceKey) != "" && strings.TrimSpace(c.cfg.WebServiceSK) != ""
}

// ReverseGeocode 根据经纬度解析省市区与标准地址，并优先使用缓存减少外部请求。
func (c *Client) ReverseGeocode(ctx context.Context, latitude, longitude float64) (*Result, error) {
	if !c.IsConfigured() {
		return nil, errors.New("腾讯位置服务未配置")
	}
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, errors.New("经纬度参数不合法")
	}
	location := formatLocation(latitude, longitude)
	cacheKey := reverseGeocodeCacheKey(location)
	var cached Result
	if ok, err := cache.GetJSON(ctx, cacheKey, &cached); err == nil && ok {
		return &cached, nil
	}

	reqURL, err := c.buildReverseGeocodeURL(location)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, safeRequestError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out reverseGeocodeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Status != 0 {
		return nil, fmt.Errorf("腾讯逆地理解析失败: %s", strings.TrimSpace(out.Message))
	}
	result := &Result{
		Province:         strings.TrimSpace(out.Result.AdInfo.Province),
		City:             strings.TrimSpace(out.Result.AdInfo.City),
		District:         strings.TrimSpace(out.Result.AdInfo.District),
		Address:          strings.TrimSpace(out.Result.Address),
		FormattedAddress: strings.TrimSpace(out.Result.Address),
		AdCode:           strings.TrimSpace(out.Result.AdInfo.AdCode),
	}
	_ = cache.SetJSON(ctx, cacheKey, result, reverseGeocodeCacheTTL)
	return result, nil
}

// buildReverseGeocodeURL 生成包含签名的腾讯逆地理请求地址。
func (c *Client) buildReverseGeocodeURL(location string) (string, error) {
	params := map[string]string{
		"key":      strings.TrimSpace(c.cfg.WebServiceKey),
		"location": location,
	}
	signature := buildSignature(reverseGeocodePath, params, strings.TrimSpace(c.cfg.WebServiceSK))
	values := url.Values{}
	values.Set("key", params["key"])
	values.Set("location", params["location"])
	values.Set("sig", signature)
	return webServiceBaseURL + reverseGeocodePath + "?" + values.Encode(), nil
}

// buildSignature 根据腾讯位置服务签名规则计算 GET 请求 sig。
func buildSignature(path string, params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+params[key])
	}
	payload := path + "?" + strings.Join(pairs, "&") + secret
	sum := md5.Sum([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// formatLocation 将经纬度格式化为腾讯接口要求的 location 参数。
func formatLocation(latitude, longitude float64) string {
	return strconv.FormatFloat(latitude, 'f', 6, 64) + "," + strconv.FormatFloat(longitude, 'f', 6, 64)
}

// reverseGeocodeCacheKey 返回坐标逆地理解析的缓存 Key。
func reverseGeocodeCacheKey(location string) string {
	return "tencent_map:reverse_geocode:" + location
}

// safeRequestError 返回不包含请求 URL 的腾讯地图错误，避免 key 或 sig 进入日志。
func safeRequestError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("腾讯位置服务请求失败: %s %v", urlErr.Op, urlErr.Err)
	}
	return fmt.Errorf("腾讯位置服务请求失败: %v", err)
}
