package datacipher

import (
	"os"
	"strings"
	"testing"
)

// TestEncryptDecryptRoundTrip 验证加密解密往返与幂等保护。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	Configure("unit-encrypt-key", "unit-hash-key", "")
	defer func() { manager = nil }()

	const secret = "13800138000"
	encrypted, err := EncryptString(secret)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	if !IsEncrypted(encrypted) {
		t.Fatalf("encrypted value should carry %q prefix", "enc:v1:")
	}
	if again, _ := EncryptString(encrypted); again != encrypted {
		t.Fatalf("re-encrypting encrypted value should be idempotent")
	}
	plain, err := DecryptString(encrypted)
	if err != nil {
		t.Fatalf("DecryptString: %v", err)
	}
	if plain != secret {
		t.Fatalf("DecryptString() = %q, want %q", plain, secret)
	}
	if empty, err := EncryptString("  "); err != nil || empty != "" {
		t.Fatalf("empty value should pass through, got %q, %v", empty, err)
	}
}

// TestDecryptLegacyPlaintext 验证历史明文直通。
func TestDecryptLegacyPlaintext(t *testing.T) {
	Configure("unit-encrypt-key", "", "")
	defer func() { manager = nil }()
	plain, err := DecryptString("legacy-plain")
	if err != nil || plain != "legacy-plain" {
		t.Fatalf("legacy plaintext should pass through, got %q, %v", plain, err)
	}
}

// TestUnconfiguredPassthrough 验证未启用态：加密为空操作、哈希退化为 SHA-256。
func TestUnconfiguredPassthrough(t *testing.T) {
	Configure("", "", "")
	defer func() { manager = nil }()
	value, err := EncryptString("13800138000")
	if err != nil || value != "13800138000" || IsEncrypted(value) {
		t.Fatalf("unconfigured encrypt should pass through, got %q, %v", value, err)
	}
	if LookupHash("x") == "" {
		t.Fatalf("unconfigured LookupHash should fall back to SHA-256")
	}
}

// TestFallbackSecret 验证 fallbackSecret 派生兜底。
func TestFallbackSecret(t *testing.T) {
	Configure("", "", "fallback-secret")
	defer func() { manager = nil }()
	encrypted, err := EncryptString("13800138000")
	if err != nil || !IsEncrypted(encrypted) {
		t.Fatalf("fallback secret should enable encryption, got %q, %v", encrypted, err)
	}
	plain, err := DecryptString(encrypted)
	if err != nil || plain != "13800138000" {
		t.Fatalf("fallback secret round-trip failed, got %q, %v", plain, err)
	}
}

// TestEnvSourcePriority 验证环境变量来源优先于参数。
func TestEnvSourcePriority(t *testing.T) {
	t.Setenv("APP_DATA_ENCRYPT_KEY", "env-encrypt-key")
	t.Setenv("APP_DATA_HASH_KEY", "env-hash-key")
	Configure("param-encrypt-key", "param-hash-key", "")
	defer func() { manager = nil }()

	encrypted, err := EncryptString("13800138000")
	if err != nil || !IsEncrypted(encrypted) {
		t.Fatalf("EncryptString: %q, %v", encrypted, err)
	}
	// 移除环境变量后用不同参数密钥重新 Configure，应无法解密，
	// 以此证明加密实际使用的是环境变量密钥而非参数密钥。
	os.Unsetenv("APP_DATA_ENCRYPT_KEY")
	os.Unsetenv("APP_DATA_HASH_KEY")
	Configure("other-encrypt-key", "other-hash-key", "")
	plain, err := DecryptString(encrypted)
	if err == nil && plain == "13800138000" {
		t.Fatalf("env key should take priority over param key")
	}
}

// TestLookupHashStable 验证查询哈希稳定与归一化。
func TestLookupHashStable(t *testing.T) {
	Configure("", "unit-hash-key", "")
	defer func() { manager = nil }()

	first := LookupHash(" Foo@Bar.COM ")
	second := LookupHash("foo@bar.com")
	if first == "" || first != second {
		t.Fatalf("LookupHash should be normalized and stable: %q vs %q", first, second)
	}
	if LookupHash("") != "" {
		t.Fatalf("LookupHash(\"\") should be empty")
	}
	// 未配置 hashKey 时退化为 SHA-256，调用不应 panic。
	manager.hashKey = nil
	if LookupHash("x") == "" {
		t.Fatalf("LookupHash fallback should still work")
	}
}

// TestMaskFunctions 验证各类脱敏边界。
func TestMaskFunctions(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"MaskName 2字", MaskName("张三"), "张*"},
		{"MaskName 3字", MaskName("张三丰"), "张*丰"},
		{"MaskName 空", MaskName("  "), ""},
		{"MaskPhone", MaskPhone("13800138000"), "138****8000"},
		{"MaskIDCard", MaskIDCard("110101199001011234"), "1101**********1234"},
		{"MaskCode", MaskCode("91110108MA01ABCX5T"), "911************X5T"},
		{"MaskCompanyName", MaskCompanyName("字节跳动有限公司"), "字节****"},
		{"MaskAddress", MaskAddress("北京市海淀区中关村大街1号"), "北京市海淀区****"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if !strings.Contains(MaskName(" short "), "*") {
		t.Fatalf("MaskName should trim input")
	}
}
