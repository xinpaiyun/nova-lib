package alipay

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"strings"
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// generateTestKeyPair 生成测试用 RSA2 密钥对并返回 PEM 配置。
func generateTestKeyPair(t *testing.T) (privateKeyPEM string, publicKeyPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key failed: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	privateKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	return privateKeyPEM, publicKeyPEM
}

// TestCreatePagePayURL 验证电脑网站支付跳转链接包含必需参数且签名可验。
func TestCreatePagePayURL(t *testing.T) {
	privateKeyPEM, publicKeyPEM := generateTestKeyPair(t)
	client := NewClient(config.AlipayConfig{
		AppID:      "2021000000000000",
		PrivateKey: privateKeyPEM,
		PublicKey:  publicKeyPEM,
		NotifyURL:  "https://example.com/v1/billing/alipay/notify",
		ReturnURL:  "https://example.com/billing",
	})
	payURL, err := client.CreatePagePayURL(PagePayRequest{
		OutTradeNo:  "LYJ202609160001",
		Subject:     "旅游记-创作者月卡",
		AmountCents: 2900,
	})
	if err != nil {
		t.Fatalf("create page pay url failed: %v", err)
	}
	parsed, err := url.Parse(payURL)
	if err != nil {
		t.Fatalf("parse pay url failed: %v", err)
	}
	if !strings.HasPrefix(payURL, defaultGatewayURL+"?") {
		t.Fatalf("pay url = %s, want gateway prefix", payURL)
	}
	query := parsed.Query()
	if query.Get("method") != "alipay.trade.page.pay" {
		t.Fatalf("method = %s, want alipay.trade.page.pay", query.Get("method"))
	}
	if query.Get("app_id") != "2021000000000000" || query.Get("sign_type") != "RSA2" {
		t.Fatalf("params = %v, want app_id and RSA2 sign_type", query)
	}
	if query.Get("notify_url") != client.cfg.NotifyURL || query.Get("return_url") != client.cfg.ReturnURL {
		t.Fatalf("params = %v, want notify_url and return_url", query)
	}
	if !strings.Contains(query.Get("biz_content"), "FAST_INSTANT_TRADE_PAY") {
		t.Fatalf("biz_content = %s, want product_code FAST_INSTANT_TRADE_PAY", query.Get("biz_content"))
	}
	if query.Get("sign") == "" {
		t.Fatal("pay url missing sign")
	}
	// 签名必须能用配置的公钥验回。
	if err := client.verifySignature(query); err != nil {
		t.Fatalf("verify sign failed: %v", err)
	}
}

// TestCreatePagePayURLValidations 验证缺参时返回错误。
func TestCreatePagePayURLValidations(t *testing.T) {
	privateKeyPEM, _ := generateTestKeyPair(t)
	client := NewClient(config.AlipayConfig{AppID: "app", PrivateKey: privateKeyPEM})
	if _, err := client.CreatePagePayURL(PagePayRequest{Subject: "s", AmountCents: 100}); err == nil {
		t.Fatal("empty out_trade_no should fail")
	}
	if _, err := client.CreatePagePayURL(PagePayRequest{OutTradeNo: "no1", Subject: "s", AmountCents: 0}); err == nil {
		t.Fatal("zero amount should fail")
	}
	if _, err := client.CreatePagePayURL(PagePayRequest{OutTradeNo: "no1", AmountCents: 100}); err == nil {
		t.Fatal("empty subject should fail")
	}
}

// TestParsePagePayReturn 验证回跳参数验签与解析。
func TestParsePagePayReturn(t *testing.T) {
	privateKeyPEM, publicKeyPEM := generateTestKeyPair(t)
	client := NewClient(config.AlipayConfig{
		AppID:      "2021000000000000",
		PrivateKey: privateKeyPEM,
		PublicKey:  publicKeyPEM,
	})
	// 用客户端私钥构造一段合法的回跳签名参数。
	params := url.Values{}
	params.Set("app_id", "2021000000000000")
	params.Set("out_trade_no", "LYJ202609160001")
	params.Set("trade_no", "202609162200140000")
	params.Set("trade_status", "TRADE_SUCCESS")
	if err := client.sign(params); err != nil {
		t.Fatalf("sign return params failed: %v", err)
	}
	result, err := client.ParsePagePayReturn(params)
	if err != nil {
		t.Fatalf("parse page pay return failed: %v", err)
	}
	if result.OutTradeNo != "LYJ202609160001" || result.TradeStatus != "TRADE_SUCCESS" {
		t.Fatalf("result = %+v, want out_trade_no and trade_status", result)
	}
	// 篡改参数后验签必须失败。
	params.Set("trade_status", "TRADE_CLOSED")
	if _, err := client.ParsePagePayReturn(params); err == nil {
		t.Fatal("tampered return params should fail verification")
	}
}
