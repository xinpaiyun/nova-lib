// Package wechat 提供微信小程序登录与微信支付 V3 的标准能力封装。
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/cache"
	"github.com/xinpaiyun/nova-lib/config"
)

const wechatAPIBase = "https://api.weixin.qq.com"

// Client 封装微信小程序服务端 API。
type Client struct {
	cfg        config.WechatConfig
	httpClient *http.Client
}

// Code2SessionResponse 定义微信 code2session 完整响应。
type Code2SessionResponse struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// PhoneNumberResponse 定义微信手机号授权响应。
type PhoneNumberResponse struct {
	ErrCode   int    `json:"errcode"`
	ErrMsg    string `json:"errmsg"`
	PhoneInfo struct {
		PhoneNumber     string `json:"phoneNumber"`
		PurePhoneNumber string `json:"purePhoneNumber"`
		CountryCode     string `json:"countryCode"`
	} `json:"phone_info"`
}

// UnlimitedQRCodeRequest 定义生成不限制数量小程序码的请求。
type UnlimitedQRCodeRequest struct {
	Scene      string `json:"scene"`
	Page       string `json:"page,omitempty"`
	CheckPath  bool   `json:"check_path"`
	EnvVersion string `json:"env_version,omitempty"`
	Width      int    `json:"width,omitempty"`
}

// SubscribeMessageData 定义订阅消息模板字段键值对。
type SubscribeMessageData map[string]struct {
	Value string `json:"value"`
}

// SubscribeMessageRequest 定义微信订阅消息发送请求。
type SubscribeMessageRequest struct {
	ToUser           string               `json:"touser"`
	TemplateID       string               `json:"template_id"`
	Page             string               `json:"page,omitempty"`
	Data             SubscribeMessageData `json:"data"`
	MiniprogramState string               `json:"miniprogram_state,omitempty"`
	Lang             string               `json:"lang,omitempty"`
}

// NewClient 创建微信小程序客户端。
func NewClient(cfg config.WechatConfig) *Client {
	return &Client{cfg: cfg, httpClient: &http.Client{Timeout: 15 * time.Second}}
}

// Enabled 返回微信小程序客户端是否已配置。
func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.cfg.AppID) != "" && strings.TrimSpace(c.cfg.AppSecret) != ""
}

// Code2Session 通过 wx.login code 换取 openid。
func (c *Client) Code2Session(loginCode string) (string, error) {
	session, err := c.Session(context.Background(), loginCode)
	if err != nil {
		return "", err
	}
	return session.OpenID, nil
}

// Session 通过 wx.login code 换取 openid、session_key 与 unionid。
func (c *Client) Session(ctx context.Context, loginCode string) (*Code2SessionResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("微信小程序未配置完成")
	}
	url := fmt.Sprintf("%s/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		wechatAPIBase, c.cfg.AppID, c.cfg.AppSecret, loginCode)
	var out Code2SessionResponse
	if err := c.getJSON(ctx, url, &out); err != nil {
		return nil, err
	}
	if out.ErrCode != 0 {
		return nil, fmt.Errorf("微信登录失败: %d %s", out.ErrCode, out.ErrMsg)
	}
	return &out, nil
}

// GetPhoneNumber 通过微信手机号授权 code 获取手机号（使用调用方提供的 access_token）。
func (c *Client) GetPhoneNumber(code string, accessToken string) (string, error) {
	return c.GetPhoneNumberWithContext(context.Background(), code, accessToken)
}

// GetPhoneNumberWithContext 通过手机号授权 code 获取手机号。
func (c *Client) GetPhoneNumberWithContext(ctx context.Context, code string, accessToken string) (string, error) {
	if !c.Enabled() {
		return "", errors.New("微信小程序未配置完成")
	}
	return c.getPhoneNumber(ctx, accessToken, code)
}

// FetchPhoneNumber 自动获取 access_token 并通过手机号授权 code 换取手机号。
func (c *Client) FetchPhoneNumber(ctx context.Context, code string) (string, error) {
	accessToken, err := c.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	return c.getPhoneNumber(ctx, accessToken, code)
}

// AccessToken 返回小程序服务端 access_token，优先读取缓存并在临期时刷新。
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	if !c.Enabled() {
		return "", errors.New("微信小程序未配置完成")
	}
	key := "wechat:access_token:" + strings.TrimSpace(c.cfg.AppID)
	if token, err := cache.Get(ctx, key); err == nil && token != "" {
		return token, nil
	}
	url := fmt.Sprintf("%s/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
		wechatAPIBase, c.cfg.AppID, c.cfg.AppSecret)
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := c.getJSON(ctx, url, &out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("微信 access_token 获取失败: %d %s", out.ErrCode, out.ErrMsg)
	}
	_ = cache.Set(ctx, key, out.AccessToken, accessTokenTTL(out.ExpiresIn))
	return out.AccessToken, nil
}

// GenerateUnlimitedQRCode 调用微信接口生成不限制数量的小程序码图片。
func (c *Client) GenerateUnlimitedQRCode(ctx context.Context, accessToken string, req UnlimitedQRCodeRequest) ([]byte, string, error) {
	if accessToken == "" {
		return nil, "", errors.New("access_token 不能为空")
	}
	if req.Scene == "" {
		return nil, "", errors.New("scene 不能为空")
	}
	if req.EnvVersion == "" {
		req.EnvVersion = "release"
	}
	if req.Width == 0 {
		req.Width = 430
	}
	url := fmt.Sprintf("%s/wxa/getwxacodeunlimit?access_token=%s", wechatAPIBase, accessToken)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, "", safeRequestError(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	contentType := resp.Header.Get("Content-Type")
	if bytes.HasPrefix(data, []byte("{")) {
		var wxErr struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		_ = json.Unmarshal(data, &wxErr)
		return nil, contentType, fmt.Errorf("微信小程序码生成失败: %d %s", wxErr.ErrCode, wxErr.ErrMsg)
	}
	return data, contentType, nil
}

// SendSubscribeMessage 调用微信订阅消息发送接口。
func (c *Client) SendSubscribeMessage(ctx context.Context, req SubscribeMessageRequest) error {
	accessToken, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	if req.MiniprogramState == "" {
		req.MiniprogramState = "formal"
	}
	if req.Lang == "" {
		req.Lang = "zh_CN"
	}
	url := fmt.Sprintf("%s/cgi-bin/message/subscribe/send?access_token=%s", wechatAPIBase, accessToken)
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return safeRequestError(err)
	}
	defer resp.Body.Close()
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("微信订阅消息发送失败: %d %s", out.ErrCode, out.ErrMsg)
	}
	return nil
}

// getPhoneNumber 调用微信接口解析手机号授权 code。
func (c *Client) getPhoneNumber(ctx context.Context, accessToken string, code string) (string, error) {
	if accessToken == "" {
		return "", errors.New("access_token 不能为空")
	}
	if code == "" {
		return "", errors.New("手机号授权 code 不能为空")
	}
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/wxa/business/getuserphonenumber?access_token=%s", wechatAPIBase, accessToken)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", safeRequestError(err)
	}
	defer resp.Body.Close()
	var out PhoneNumberResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 {
		return "", fmt.Errorf("微信手机号获取失败: %d %s", out.ErrCode, out.ErrMsg)
	}
	if out.PhoneInfo.PurePhoneNumber != "" {
		return out.PhoneInfo.PurePhoneNumber, nil
	}
	return out.PhoneInfo.PhoneNumber, nil
}

// getJSON 发送 GET 请求并解析 JSON 响应。
func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return safeRequestError(err)
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// accessTokenTTL 根据微信返回有效期预留 10 分钟刷新窗口。
func accessTokenTTL(expiresIn int64) time.Duration {
	if expiresIn <= 600 {
		return 100 * time.Minute
	}
	return time.Duration(expiresIn-600) * time.Second
}

// safeRequestError 返回不包含请求 URL 的微信外部接口错误，避免 secret 或 access_token 进入日志。
func safeRequestError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *neturl.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("微信接口请求失败: %s %v", urlErr.Op, urlErr.Err)
	}
	return fmt.Errorf("微信接口请求失败: %v", err)
}
