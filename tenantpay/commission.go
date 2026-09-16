package tenantpay

// CalcCommission 计算订单抽成金额（分）。
// 规则：抽成 = max(实付金额 × 比例, 单笔保底)，且不超过实付金额；
// 比例部分按整数分向下取整（不足一分的零头留给商户）。
// 未启用抽成或实付金额 <= 0 时返回 0。
func CalcCommission(paidCent int64, cfg TenantCommission) int64 {
	if !cfg.Enabled || paidCent <= 0 {
		return 0
	}
	byRate := paidCent * cfg.RateBP / 10000
	amount := byRate
	if cfg.MinCommissionCent > byRate {
		amount = cfg.MinCommissionCent
	}
	if amount > paidCent {
		amount = paidCent
	}
	if amount < 0 {
		return 0
	}
	return amount
}
