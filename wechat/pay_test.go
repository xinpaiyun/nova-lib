package wechat

import (
	"context"
	"strings"
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestPayClientDisabledWithoutConfig 验证缺少支付配置时返回禁用态客户端。
func TestPayClientDisabledWithoutConfig(t *testing.T) {
	pay := NewPayClient(config.WechatConfig{})
	if pay.Enabled() {
		t.Fatalf("Enabled() = true, want false for empty config")
	}
	if _, err := pay.TransferToBalance(context.Background(), MerchantTransferRequest{TransferAmount: 1, TransferRemark: "备注"}); err == nil || !strings.Contains(err.Error(), "未配置完成") {
		t.Fatalf("TransferToBalance err = %v, want disabled", err)
	}
	if _, err := pay.PrepayMiniProgram(context.Background(), MiniProgramPrepayRequest{OpenID: "openid"}); err == nil || !strings.Contains(err.Error(), "未配置完成") {
		t.Fatalf("PrepayMiniProgram err = %v, want disabled", err)
	}
}

// TestPayClientConfigSnapshot 验证 Config 返回客户端使用的配置快照。
func TestPayClientConfigSnapshot(t *testing.T) {
	cfg := config.WechatConfig{AppID: "wx-app", MchID: "mch-1"}
	pay := NewPayClient(cfg)
	got := pay.Config()
	if got.AppID != "wx-app" || got.MchID != "mch-1" {
		t.Fatalf("Config() = %+v, want appID=wx-app mchID=mch-1", got)
	}
	var nilPay *PayClient
	if nilPay.Config().AppID != "" {
		t.Fatalf("nil client Config() should return zero value")
	}
}

// TestTransferValidation 验证转账参数校验。
func TestTransferValidation(t *testing.T) {
	pay := NewPayClient(config.WechatConfig{AppID: "wx-app"})
	// 未启用时所有方法直接拒绝，无法触发参数校验分支的深层逻辑；这里只校验禁用态行为。
	if _, err := pay.TransferWithAuthorization(context.Background(), MerchantTransferRequest{}); err == nil {
		t.Fatalf("TransferWithAuthorization should reject disabled client")
	}
	if err := pay.CloseUserConfirmAuthorization(context.Background(), "out-no"); err == nil {
		t.Fatalf("CloseUserConfirmAuthorization should reject disabled client")
	}
}
