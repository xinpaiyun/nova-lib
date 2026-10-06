package scenario

import (
	"strings"
	"testing"
)

// TestConstantsFormat 校验编码常量格式（两级、全小写、中划线分隔）。
func TestConstantsFormat(t *testing.T) {
	for _, code := range []string{
		DiningMilkTea, DiningDineIn,
		VehicleRepair, VehicleMaintenance, VehicleBeauty,
		PetHospital, PetGrooming, PetBoarding,
		FreshECommerce, FreshStore,
	} {
		if !strings.Contains(code, ".") {
			t.Errorf("二级编码 %q 缺少一级前缀", code)
		}
		if code != strings.ToLower(code) {
			t.Errorf("编码 %q 含大写字符", code)
		}
		if strings.ContainsAny(code, " _") {
			t.Errorf("编码 %q 含空格或下划线（应使用中划线）", code)
		}
	}
	for _, code := range []string{Dining, Vehicle, Pet, Fresh} {
		if strings.Contains(code, ".") || code != strings.ToLower(code) {
			t.Errorf("一级编码 %q 格式不正确", code)
		}
	}
}

func TestSplit(t *testing.T) {
	if first, sub, ok := Split(DiningMilkTea); !ok || first != Dining || sub != "milk-tea" {
		t.Fatalf("Split 拆分不正确: %q %q %v", first, sub, ok)
	}
	if first, sub, ok := Split(Dining); !ok || first != Dining || sub != "" {
		t.Fatalf("仅一级编码应拆出空二级: %q %q %v", first, sub, ok)
	}
	if _, _, ok := Split(""); ok {
		t.Fatal("空编码应返回 false")
	}
}
