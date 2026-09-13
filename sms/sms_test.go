package sms

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v4/client"
	"github.com/xinpaiyun/nova-lib/cache"
	"github.com/xinpaiyun/nova-lib/config"
)

// TestMain 初始化本地缓存后端，供验证码读写测试使用。
func TestMain(m *testing.M) {
	dbPath, err := os.MkdirTemp("", "sms-test-cache")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dbPath)
	if err := cache.InitLocal(dbPath + "/cache.db"); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = cache.CloseLocal()
	os.Exit(code)
}

// TestVerifyCodeWithConfigConsumesCode 验证短信验证码校验成功后会被一次性消费。
func TestVerifyCodeWithConfigConsumesCode(t *testing.T) {
	ctx := context.Background()
	phone := "13900001111"
	code := "123456"
	if err := cache.Set(ctx, codeKeyPrefix+phone, code, time.Minute); err != nil {
		t.Fatalf("cache.Set() error = %v", err)
	}
	cfg := config.SMSConfig{
		Enabled:         true,
		Provider:        "aliyun",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
		SignName:        "Nova",
		TemplateCode:    "SMS_000001",
	}
	if !VerifyCodeWithConfig(ctx, cfg, phone, code) {
		t.Fatal("first VerifyCodeWithConfig() = false, want true")
	}
	if VerifyCodeWithConfig(ctx, cfg, phone, code) {
		t.Fatal("second VerifyCodeWithConfig() = true, want false after consume")
	}
}

// TestValidateSendSmsResult 验证网关返回业务错误码时发送报错而非静默成功。
// 阿里云业务失败时 HTTP 仍返回 200，必须校验响应体 Code 字段。
func TestValidateSendSmsResult(t *testing.T) {
	okResp := &dysmsapi.SendSmsResponse{Body: &dysmsapi.SendSmsResponseBody{
		Code:    stringPtr("OK"),
		Message: stringPtr("OK"),
	}}
	if err := validateSendSmsResult(okResp); err != nil {
		t.Fatalf("validateSendSmsResult() with Code=OK error = %v, want nil", err)
	}

	failResp := &dysmsapi.SendSmsResponse{Body: &dysmsapi.SendSmsResponseBody{
		Code:    stringPtr("isv.SMS_SIGNATURE_ILLEGAL"),
		Message: stringPtr("短信签名不合法"),
	}}
	err := validateSendSmsResult(failResp)
	if err == nil {
		t.Fatal("validateSendSmsResult() with business error code = nil, want error")
	}
	if want := "isv.SMS_SIGNATURE_ILLEGAL"; !containsStr(err.Error(), want) {
		t.Fatalf("validateSendSmsResult() error = %q, want containing %q", err.Error(), want)
	}

	if err := validateSendSmsResult(nil); err == nil {
		t.Fatal("validateSendSmsResult(nil) = nil, want error")
	}
	if err := validateSendSmsResult(&dysmsapi.SendSmsResponse{}); err == nil {
		t.Fatal("validateSendSmsResult() with nil body = nil, want error")
	}
}

// containsStr 判断子串是否包含。
func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}
