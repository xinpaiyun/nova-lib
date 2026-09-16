// Package billing 提供会员"套餐+价格档"模型的通用纯逻辑：
// 价格档校验、有效期计算与月付/年付周期约定。
// 不包含 GORM 模型与存储实现，各项目自行定义套餐/订单表结构后复用本包校验与计算规则。
package billing

import (
	"errors"
	"fmt"
	"time"
)

// 套餐类型约定：plan = 订阅套餐（可挂多个周期价格档），addon = 加量包（恰好一档长期价）。
const (
	KindPlan  = "plan"
	KindAddon = "addon"
)

// 周期档位约定：有效期 ≤62 天展示为"月付"，≥360 天展示为"年付"，
// 其余按具体天数展示；0 表示长期（加量包固定为 0）。
const (
	MonthCycleMaxDays = 62
	YearCycleMinDays  = 360
)

// longTermYear 长期有效期的表示年份（9999-12-31 23:59:59 Asia/Shanghai）。
const longTermYear = 9999

// PriceTier 描述一个价格档：有效期天数 + 价格（分）。
type PriceTier struct {
	DurationDays int
	PriceCents   int64
}

// ValidatePriceTiers 校验套餐价格档列表：
// 价格与有效期非负、同套餐内有效期不重复；
// 订阅套餐至少一档，加量包恰好一档且有效期为 0（长期）。
// 错误信息与各项目管理端文案保持一致，可直接返回给客户端。
func ValidatePriceTiers(kind string, tiers []PriceTier) error {
	seenDurations := make(map[int]bool, len(tiers))
	for _, tier := range tiers {
		if tier.PriceCents < 0 {
			return errors.New("价格不能为负数")
		}
		if tier.DurationDays < 0 {
			return errors.New("有效期不能为负数")
		}
		if kind == KindAddon && tier.DurationDays != 0 {
			return errors.New("加量包价格档有效期必须为 0（长期有效）")
		}
		if seenDurations[tier.DurationDays] {
			return errors.New("同一套餐下有效期不能重复")
		}
		seenDurations[tier.DurationDays] = true
	}
	if kind == KindPlan && len(tiers) == 0 {
		return errors.New("订阅套餐至少需要设置一个价格档")
	}
	if kind == KindAddon && len(tiers) != 1 {
		return errors.New("加量包只能设置一个长期价格档")
	}
	return nil
}

// ExpiredAt 按购买时长计算到期时间；
// durationDays <= 0 表示长期有效，返回 9999-12-31 23:59:59（Asia/Shanghai）。
func ExpiredAt(durationDays int, now time.Time) time.Time {
	if durationDays <= 0 {
		return time.Date(longTermYear, time.December, 31, 23, 59, 59, 0, ShanghaiLocation)
	}
	return now.AddDate(0, 0, durationDays)
}

// ExtendExpiredAt 同套餐续费时在原到期时间上顺延购买时长；
// 长期有效（9999 年）续费后仍为长期。
func ExtendExpiredAt(current time.Time, durationDays int) time.Time {
	if IsLongTerm(current) {
		return current
	}
	return current.AddDate(0, 0, durationDays)
}

// IsLongTerm 判断到期时间是否为长期有效（9999 年）。
func IsLongTerm(expiredAt time.Time) bool {
	return expiredAt.Year() >= longTermYear
}

// CycleLabel 返回价格档的周期展示标签：
// 0 天返回空串（加量包无周期），≤62 天为"月付"，≥360 天为"年付"，其余为"X天"。
func CycleLabel(durationDays int) string {
	switch {
	case durationDays <= 0:
		return ""
	case durationDays >= YearCycleMinDays:
		return "年付"
	case durationDays <= MonthCycleMaxDays:
		return "月付"
	default:
		return fmt.Sprintf("%d天", durationDays)
	}
}

// ShanghaiLocation 中国标准时区，供长期有效期计算使用。
var ShanghaiLocation = time.FixedZone("Asia/Shanghai", 8*3600)
