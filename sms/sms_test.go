package sms

import (
	"context"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/cache"
)

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
