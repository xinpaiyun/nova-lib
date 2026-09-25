// Package settle 提供结算抵扣编排：先核销优惠券、再扣减会员余额，返回实收金额。
// 通过最小接口依赖会员/优惠券能力；各项目可传入自建实现或 nova-lib 参考引擎
// （member.NewEngine 的 Wallet 与 coupon.NewEngine 的 Engine 均满足对应接口）。
package settle

import (
	"context"

	"github.com/xinpaiyun/nova-lib/member"
)

// BalanceDeducter 余额抵扣能力（member.Wallet 满足该接口）。
type BalanceDeducter interface {
	Deduct(ctx context.Context, tenantID, holderID uint64, amount int64, ref member.Ref) (int64, error)
}

// CouponClaimer 优惠券核销能力（coupon.Claimer 满足该接口）。
type CouponClaimer interface {
	Claim(ctx context.Context, tenantID, holderID, couponID uint64, orderAmount int64, ref member.Ref) (int64, string, error)
}

// Input 结算抵扣入参。
type Input struct {
	TenantID    uint64
	HolderID    uint64
	TotalAmount int64      // 应收金额（分）
	CouponID    uint64     // >0 时先核销优惠券
	UseBalance  bool       // 是否使用余额抵扣
	Ref         member.Ref // 关联业务单据（核券与扣余额共用）
}

// Result 结算抵扣结果。
type Result struct {
	CouponID       uint64
	CouponName     string
	CouponDiscount int64 // 券抵扣金额（分）
	BalanceUsed    int64 // 余额实扣金额（分）
	PaidAmount     int64 // 实收金额（分）
}

// Apply 执行结算抵扣编排：先核券后扣余额，返回实收。
// CouponID>0 时先核销优惠券（券失败直接返回错误）；
// UseBalance 且券后待付大于 0 时扣减余额（实扣不超过余额）；
// PaidAmount = TotalAmount - CouponDiscount - BalanceUsed。
func Apply(ctx context.Context, in Input, coupons CouponClaimer, balance BalanceDeducter) (Result, error) {
	var result Result
	payable := in.TotalAmount
	if in.CouponID > 0 {
		discount, name, err := coupons.Claim(ctx, in.TenantID, in.HolderID, in.CouponID, in.TotalAmount, in.Ref)
		if err != nil {
			return Result{}, err
		}
		result.CouponID = in.CouponID
		result.CouponName = name
		result.CouponDiscount = discount
		payable -= discount
	}
	if in.UseBalance && payable > 0 {
		used, err := balance.Deduct(ctx, in.TenantID, in.HolderID, payable, in.Ref)
		if err != nil {
			return Result{}, err
		}
		result.BalanceUsed = used
	}
	result.PaidAmount = in.TotalAmount - result.CouponDiscount - result.BalanceUsed
	return result, nil
}
