package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v4/client"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/cache"
)

const (
	codeKeyPrefix  = "sms:code:"
	codeExpiration = 5 * time.Minute
	sendInterval   = 60 * time.Second
)

var client *dysmsapi.Client
var cfg config.SMSConfig

// Init 初始化短信服务，未启用时进入开发模式。
func Init(c config.SMSConfig) error {
	cfg = c
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.Provider) != "aliyun" {
		return fmt.Errorf("unsupported sms provider: %s", cfg.Provider)
	}
	created, err := newAliyunClient(cfg)
	if err != nil {
		return fmt.Errorf("create aliyun sms client failed: %w", err)
	}
	client = created
	return nil
}

// Enabled 返回短信服务是否已启用。
func Enabled() bool {
	return EnabledWithConfig(cfg)
}

// EnabledWithConfig 判断指定短信配置是否可用于真实发送。
func EnabledWithConfig(c config.SMSConfig) bool {
	return c.Enabled &&
		strings.TrimSpace(c.Provider) == "aliyun" &&
		strings.TrimSpace(c.AccessKeyID) != "" &&
		strings.TrimSpace(c.AccessKeySecret) != "" &&
		strings.TrimSpace(c.SignName) != "" &&
		strings.TrimSpace(c.TemplateCode) != ""
}

// SendCode 发送短信验证码，开发模式下仅写入缓存。
func SendCode(ctx context.Context, phone string) (string, error) {
	return SendCodeWithConfig(ctx, cfg, client, phone)
}

// SendCodeWithConfig 使用指定短信配置发送验证码，配置不可发送时进入开发模式。
func SendCodeWithConfig(ctx context.Context, c config.SMSConfig, sender *dysmsapi.Client, phone string) (string, error) {
	code := generateCode()
	rateKey := codeKeyPrefix + "rate:" + phone
	if _, err := cache.Get(ctx, rateKey); err == nil {
		return "", fmt.Errorf("验证码发送过于频繁，请 %d 秒后再试", int(sendInterval.Seconds()))
	}
	if err := SendTemplateWithConfig(ctx, c, sender, phone, c.TemplateCode, map[string]string{"code": code}); err != nil {
		return "", err
	}
	if err := cache.Set(ctx, codeKeyPrefix+phone, code, codeExpiration); err != nil {
		return "", err
	}
	_ = cache.Set(ctx, rateKey, "1", sendInterval)
	return code, nil
}

// SendTemplateWithConfig 使用指定短信配置发送模板短信，配置不可发送时进入开发模式。
func SendTemplateWithConfig(ctx context.Context, c config.SMSConfig, sender *dysmsapi.Client, phone string, templateCode string, params map[string]string) error {
	_ = ctx
	phone = strings.TrimSpace(phone)
	templateCode = strings.TrimSpace(templateCode)
	if phone == "" {
		return fmt.Errorf("请输入手机号")
	}
	if templateCode == "" {
		templateCode = strings.TrimSpace(c.TemplateCode)
	}
	sendCfg := c
	sendCfg.TemplateCode = templateCode
	if !EnabledWithConfig(sendCfg) {
		return nil
	}
	if params == nil {
		params = map[string]string{}
	}
	target := sender
	if target == nil || sendCfg != cfg {
		created, err := newAliyunClient(sendCfg)
		if err != nil {
			return fmt.Errorf("短信发送失败，请稍后重试")
		}
		target = created
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("短信模板参数不正确")
	}
	req := &dysmsapi.SendSmsRequest{
		PhoneNumbers:  stringPtr(phone),
		SignName:      stringPtr(sendCfg.SignName),
		TemplateCode:  stringPtr(templateCode),
		TemplateParam: stringPtr(string(payload)),
	}
	if _, err := target.SendSms(req); err != nil {
		return fmt.Errorf("短信发送失败，请稍后重试")
	}
	return nil
}

// VerifyCode 校验短信验证码，未启用短信时允许 000000 作为开发验证码。
func VerifyCode(ctx context.Context, phone, code string) bool {
	return VerifyCodeWithConfig(ctx, cfg, phone, code)
}

// VerifyCodeWithConfig 使用指定配置校验验证码，开发模式允许 000000。
func VerifyCodeWithConfig(ctx context.Context, c config.SMSConfig, phone, code string) bool {
	if !EnabledWithConfig(c) && code == "000000" {
		return true
	}
	stored, err := cache.GetDel(ctx, codeKeyPrefix+phone)
	if err != nil || stored != code {
		return false
	}
	return true
}

// generateCode 生成 6 位数字验证码。
func generateCode() string {
	return fmt.Sprintf("%06d", rand.Intn(1000000))
}

// newAliyunClient 创建阿里云短信客户端。
func newAliyunClient(c config.SMSConfig) (*dysmsapi.Client, error) {
	openAPICfg := &openapi.Config{
		AccessKeyId:     &c.AccessKeyID,
		AccessKeySecret: &c.AccessKeySecret,
		Endpoint:        stringPtr("dysmsapi.aliyuncs.com"),
	}
	return dysmsapi.NewClient(openAPICfg)
}

// stringPtr 返回字符串指针。
func stringPtr(value string) *string {
	return &value
}
