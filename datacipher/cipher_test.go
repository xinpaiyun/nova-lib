package datacipher

import (
	"strings"
	"testing"
)

func TestConfigureAndEncryptDecryptRoundTrip(t *testing.T) {
	Configure("test-encrypt-key", "test-hash-key", "")
	cipherText, err := EncryptString("敏感身份证号码")
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if !IsEncrypted(cipherText) {
		t.Fatalf("IsEncrypted = false for cipherText")
	}
	plain, err := DecryptString(cipherText)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if plain != "敏感身份证号码" {
		t.Fatalf("decrypt = %q, want original", plain)
	}
}

func TestEncryptIdempotent(t *testing.T) {
	Configure("key", "hash", "")
	once, _ := EncryptString("idempotent")
	twice, _ := EncryptString(once)
	if once != twice {
		t.Fatalf("EncryptString(idempotent) = %q != %q", twice, once)
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	Configure("key", "hash", "")
	plain, err := DecryptString("明文直接返回")
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if plain != "明文直接返回" {
		t.Fatalf("decrypt = %q", plain)
	}
}

func TestMaskName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"张", "*"},
		{"张三", "张*"},
		{"张三丰", "张*丰"},
		{"欧阳锋锋", "欧**锋"},
	}
	for _, c := range cases {
		if got := MaskName(c.in); got != c.want {
			t.Errorf("MaskName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	if got := MaskPhone("13812345678"); got != "138****5678" {
		t.Fatalf("MaskPhone = %q", got)
	}
}

func TestMaskIDCard(t *testing.T) {
	if got := MaskIDCard("110101199001011234"); !strings.HasPrefix(got, "1101") || !strings.HasSuffix(got, "1234") {
		t.Fatalf("MaskIDCard = %q", got)
	}
}

func TestLookupHash(t *testing.T) {
	Configure("key", "hash", "")
	h1 := LookupHash("  Test@Example.com  ")
	h2 := LookupHash("test@example.com")
	if h1 == "" || h1 != h2 {
		t.Fatalf("LookupHash normalize failed: h1=%q h2=%q", h1, h2)
	}
}
