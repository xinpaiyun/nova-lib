package tenantpay

import "testing"

func TestCalcCommission(t *testing.T) {
	cases := []struct {
		name   string
		paid   int64
		cfg    TenantCommission
		expect int64
	}{
		{
			name:   "按比例抽成",
			paid:   10000, // 100 元
			cfg:    TenantCommission{Enabled: true, RateBP: 500, MinCommissionCent: 0},
			expect: 500, // 5%
		},
		{
			name:   "保底金额生效",
			paid:   100, // 1 元，5% = 5 分，保底 30 分
			cfg:    TenantCommission{Enabled: true, RateBP: 500, MinCommissionCent: 30},
			expect: 30,
		},
		{
			name:   "比例高于保底",
			paid:   10000,
			cfg:    TenantCommission{Enabled: true, RateBP: 500, MinCommissionCent: 30},
			expect: 500,
		},
		{
			name:   "抽成不超过实付",
			paid:   20,
			cfg:    TenantCommission{Enabled: true, RateBP: 5000, MinCommissionCent: 100},
			expect: 20,
		},
		{
			name:   "未启用返回 0",
			paid:   10000,
			cfg:    TenantCommission{Enabled: false, RateBP: 500, MinCommissionCent: 100},
			expect: 0,
		},
		{
			name:   "比例取整向下",
			paid:   101,
			cfg:    TenantCommission{Enabled: true, RateBP: 500},
			expect: 5, // 101*500/10000 = 5.05 → 5
		},
		{
			name:   "实付为 0",
			paid:   0,
			cfg:    TenantCommission{Enabled: true, RateBP: 500},
			expect: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CalcCommission(tc.paid, tc.cfg); got != tc.expect {
				t.Fatalf("CalcCommission(%d) = %d, want %d", tc.paid, got, tc.expect)
			}
		})
	}
}

func TestFormatRateBP(t *testing.T) {
	cases := map[int64]string{
		500:  "5.00",
		0:    "0.00",
		35:   "0.35",
		1234: "12.34",
	}
	for bp, want := range cases {
		if got := formatRateBP(bp); got != want {
			t.Fatalf("formatRateBP(%d) = %q, want %q", bp, got, want)
		}
	}
}
