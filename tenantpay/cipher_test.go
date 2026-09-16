package tenantpay

import (
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	key := "test-master-key-1234567890abcdef"
	plaintext := "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----"
	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if ciphertext == plaintext {
		t.Fatal("密文不应等于明文")
	}
	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("解密结果不匹配: got %q want %q", decrypted, plaintext)
	}
}

func TestCipherEmptyPlaintext(t *testing.T) {
	out, err := Encrypt("key-1234567890123456789012345678901", "")
	if err != nil || out != "" {
		t.Fatalf("空明文应原样返回: out=%q err=%v", out, err)
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	// 非 enc.v1: 前缀的值原样返回（兼容明文历史数据）。
	got, err := Decrypt("key-1234567890123456789012345678901", "raw-secret")
	if err != nil || got != "raw-secret" {
		t.Fatalf("明文应原样返回: got=%q err=%v", got, err)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := "key1-123456789012345678901234567890x"
	key2 := "key2-123456789012345678901234567890x"
	ciphertext, err := Encrypt(key1, "secret")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if _, err := Decrypt(key2, ciphertext); err == nil {
		t.Fatal("错误密钥应解密失败")
	}
}
