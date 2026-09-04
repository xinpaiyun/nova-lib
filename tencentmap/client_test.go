package tencentmap

import (
	"testing"
)

// TestBuildSignature 验证腾讯位置服务 sig 计算规则（path?排序参数+SK 的 MD5）。
func TestBuildSignature(t *testing.T) {
	params := map[string]string{"key": "K", "location": "39.9,116.4"}
	got := buildSignature("/ws/geocoder/v1", params, "SECRET")
	// payload = "/ws/geocoder/v1?key=K&location=39.9,116.4SECRET"
	want := "1fded50418b607a617d576af56a1770c"
	if got != want {
		t.Fatalf("buildSignature() = %q, want %q", got, want)
	}
}

// TestFormatLocation 验证经纬度格式化精度。
func TestFormatLocation(t *testing.T) {
	if got := formatLocation(39.90469, 116.40717); got != "39.904690,116.407170" {
		t.Fatalf("formatLocation() = %q", got)
	}
}
