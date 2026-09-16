package billing

import (
	"testing"
	"time"
)

// TestValidatePriceTiers 覆盖价格档校验规则：非负、去重、订阅至少一档、加量包恰好一档长期。
func TestValidatePriceTiers(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		tiers   []PriceTier
		wantErr string
	}{
		{name: "订阅套餐月付年付", kind: KindPlan, tiers: []PriceTier{{31, 2900}, {365, 26800}}},
		{name: "订阅免费长期单档", kind: KindPlan, tiers: []PriceTier{{0, 0}}},
		{name: "加量包长期单档", kind: KindAddon, tiers: []PriceTier{{0, 990}}},
		{name: "空类型默认按订阅处理", kind: "", tiers: []PriceTier{{31, 2900}}},
		{name: "价格为负", kind: KindPlan, tiers: []PriceTier{{31, -1}}, wantErr: "价格不能为负数"},
		{name: "有效期为负", kind: KindPlan, tiers: []PriceTier{{-1, 100}}, wantErr: "有效期不能为负数"},
		{name: "有效期重复", kind: KindPlan, tiers: []PriceTier{{31, 2900}, {31, 26800}}, wantErr: "同一套餐下有效期不能重复"},
		{name: "订阅套餐无价格档", kind: KindPlan, wantErr: "订阅套餐至少需要设置一个价格档"},
		{name: "加量包带周期", kind: KindAddon, tiers: []PriceTier{{31, 990}}, wantErr: "加量包价格档有效期必须为 0（长期有效）"},
		{name: "加量包多档同长期", kind: KindAddon, tiers: []PriceTier{{0, 990}, {0, 991}}, wantErr: "同一套餐下有效期不能重复"},
		{name: "加量包无价格档", kind: KindAddon, wantErr: "加量包只能设置一个长期价格档"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePriceTiers(tc.kind, tc.tiers)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestExpiredAt 验证按时长计算到期时间与长期有效期表示。
func TestExpiredAt(t *testing.T) {
	now := time.Date(2026, time.September, 16, 10, 0, 0, 0, ShanghaiLocation)
	if got := ExpiredAt(31, now); !got.Equal(now.AddDate(0, 0, 31)) {
		t.Fatalf("ExpiredAt(31) = %v, want %v", got, now.AddDate(0, 0, 31))
	}
	longTerm := time.Date(9999, time.December, 31, 23, 59, 59, 0, ShanghaiLocation)
	for _, days := range []int{0, -1} {
		if got := ExpiredAt(days, now); !got.Equal(longTerm) {
			t.Fatalf("ExpiredAt(%d) = %v, want long term %v", days, got, longTerm)
		}
	}
}

// TestExtendExpiredAt 验证同套餐续费顺延与长期有效不变。
func TestExtendExpiredAt(t *testing.T) {
	current := time.Date(2026, time.September, 16, 10, 0, 0, 0, ShanghaiLocation)
	if got := ExtendExpiredAt(current, 365); !got.Equal(current.AddDate(0, 0, 365)) {
		t.Fatalf("ExtendExpiredAt = %v, want %v", got, current.AddDate(0, 0, 365))
	}
	if got := ExtendExpiredAt(ExpiredAt(0, current), 31); !IsLongTerm(got) {
		t.Fatalf("ExtendExpiredAt(long term) = %v, want long term", got)
	}
}

// TestCycleLabel 验证月付/年付/天数/长期的周期标签约定。
func TestCycleLabel(t *testing.T) {
	cases := map[int]string{
		0:   "",
		-5:  "",
		31:  "月付",
		62:  "月付",
		63:  "63天",
		180: "180天",
		360: "年付",
		365: "年付",
	}
	for days, want := range cases {
		if got := CycleLabel(days); got != want {
			t.Fatalf("CycleLabel(%d) = %q, want %q", days, got, want)
		}
	}
}
