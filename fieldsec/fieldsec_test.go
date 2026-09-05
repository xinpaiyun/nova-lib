package fieldsec

import (
	"strings"
	"testing"
)

// TestEncryptDecryptRoundTrip 验证加密解密往返与幂等保护。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	Configure("unit-encrypt-key", "unit-hash-key")
	defer func() { encryptKey, hashKey = nil, nil }()

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
	Configure("unit-encrypt-key", "")
	defer func() { encryptKey = nil }()
	plain, err := DecryptString("legacy-plain")
	if err != nil || plain != "legacy-plain" {
		t.Fatalf("legacy plaintext should pass through, got %q, %v", plain, err)
	}
}

// TestLookupHashStable 验证查询哈希稳定与归一化。
func TestLookupHashStable(t *testing.T) {
	Configure("", "unit-hash-key")
	defer func() { encryptKey, hashKey = nil, nil }()

	first := LookupHash(" Foo@Bar.COM ")
	second := LookupHash("foo@bar.com")
	if first == "" || first != second {
		t.Fatalf("LookupHash should be normalized and stable: %q vs %q", first, second)
	}
	if LookupHash("") != "" {
		t.Fatalf("LookupHash(\"\") should be empty")
	}
	// 未配置 hashKey 时退化为 SHA-256，调用不应 panic。
	hashKey = nil
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
		{"MaskPhone", MaskPhone("13800138000"), "138****0000"},
		{"MaskIDCard", MaskIDCard("110101199001011234"), "1101****1234"},
		{"MaskCode", MaskCode("91110108MA01ABCX5T"), "911****X5T"},
		{"MaskCompanyName", MaskCompanyName("字节跳动有限公司"), "字节****"},
		{"MaskAddress", MaskAddress("北京市海淀区中关村大街1号"), "北京市海淀区中***"},
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
