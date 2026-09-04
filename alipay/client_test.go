package alipay

import (
	"net/url"
	"testing"
)

// TestSignContentSortsAndSkipsSign 验证待签名字符串按 key 排序且排除 sign 与空值。
func TestSignContentSortsAndSkipsSign(t *testing.T) {
	params := url.Values{}
	params.Set("app_id", "2021000")
	params.Set("method", "alipay.trade.create")
	params.Set("sign", "should-be-excluded")
	params.Set("empty", "")
	params.Set("charset", "utf-8")

	got := signContent(params)
	want := "app_id=2021000&charset=utf-8&method=alipay.trade.create"
	if got != want {
		t.Fatalf("signContent() = %q, want %q", got, want)
	}
}

// TestCentsToYuan 验证分转元字符串格式。
func TestCentsToYuan(t *testing.T) {
	cases := map[int64]string{1: "0.01", 100: "1.00", 12345: "123.45", 999999: "9999.99"}
	for cents, want := range cases {
		if got := centsToYuan(cents); got != want {
			t.Fatalf("centsToYuan(%d) = %q, want %q", cents, got, want)
		}
	}
}

// TestNormalizePhoneEncryptedData 验证从多种报文中提取 response 密文。
func TestNormalizePhoneEncryptedData(t *testing.T) {
	if got := normalizePhoneEncryptedData(" raw-cipher "); got != "raw-cipher" {
		t.Fatalf("plain text = %q", got)
	}
	jsonPayload := `{"response":"cipher-1","sign":"s"}`
	if got := normalizePhoneEncryptedData(jsonPayload); got != "cipher-1" {
		t.Fatalf("json response = %q", got)
	}
	encryptedPayload := `{"encryptedData":"cipher-2"}`
	if got := normalizePhoneEncryptedData(encryptedPayload); got != "cipher-2" {
		t.Fatalf("encryptedData = %q", got)
	}
}
