package wechat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/xinpaiyun/nova-lib/config"
)

// Client 封装微信小程序服务端 API。
type Client struct {
	cfg config.WechatConfig
}

// NewClient 创建微信小程序客户端。
func NewClient(cfg config.WechatConfig) *Client {
	return &Client{cfg: cfg}
}

// Enabled 返回微信小程序客户端是否已配置。
func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.cfg.AppID) != "" && strings.TrimSpace(c.cfg.AppSecret) != ""
}

// Code2Session 通过 wx.login code 换取 openid。
func (c *Client) Code2Session(loginCode string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("微信小程序未配置完成")
	}
	url := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		c.cfg.AppID,
		c.cfg.AppSecret,
		loginCode,
	)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("微信登录失败：%s", result.ErrMsg)
	}
	return result.OpenID, nil
}

// GetPhoneNumber 通过微信手机号授权 code 获取手机号。
func (c *Client) GetPhoneNumber(code string, accessToken string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("微信小程序未配置完成")
	}
	url := fmt.Sprintf("https://api.weixin.qq.com/wxa/business/getuserphonenumber?access_token=%s", accessToken)
	payload, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		ErrCode   int    `json:"errcode"`
		ErrMsg    string `json:"errmsg"`
		PhoneInfo struct {
			PurePhoneNumber string `json:"purePhoneNumber"`
		} `json:"phone_info"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("微信手机号授权失败：%s", result.ErrMsg)
	}
	return result.PhoneInfo.PurePhoneNumber, nil
}
