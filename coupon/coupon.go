// Package coupon 提供优惠券中台契约与参考引擎：模板管理、批量发放与结算核销。
// 契约（Claimer）仅覆盖结算核销，供自建实现的项目适配；参考引擎（NewGormStore + NewEngine）
// 额外提供发放、查询与模板管理，供新项目直接复用。
// 数据隔离键统一为 TenantID（列 tenant_id），持有者统一为 HolderID
// （C 端用户或客户 ID，语义由项目决定），金额一律为分（int64）。
package coupon

import (
	"context"
	"errors"
	"time"

	"github.com/xinpaiyun/nova-lib/member"
)

// Ref 关联业务单据，复用会员包定义。
type Ref = member.Ref

// 优惠券错误。
var (
	ErrTemplateNotFound   = errors.New("优惠券模板不存在")
	ErrTemplateHasCoupons = errors.New("模板已发放优惠券，不能删除")
	ErrTemplateDisabled   = errors.New("模板已停用，不能发放")
	ErrIssueLimit         = errors.New("发放数量超出模板剩余可发数量")
	ErrCouponNotFound     = errors.New("优惠券不存在")
	ErrCouponNotAvailable = errors.New("优惠券不可用")
)

// 模板与发放的入参校验错误。
var (
	ErrEmptyName         = errors.New("优惠券名称不能为空")
	ErrInvalidAmount     = errors.New("优惠券面额不正确")
	ErrInvalidMinSpend   = errors.New("使用门槛金额不正确")
	ErrInvalidValidDays  = errors.New("有效天数需在 1-3650 之间")
	ErrInvalidTotalCount = errors.New("发放上限不正确")
	ErrInvalidStatus     = errors.New("模板状态不正确")
	ErrEmptyHolders      = errors.New("请选择要发放的持有者")
	ErrTooManyHolders    = errors.New("单次发放数量超出上限")
)

// Claimer 结算核销契约：核销优惠券返回抵扣金额与券名快照。
// 自建实现的项目只需满足该接口即可接入 settle 结算编排。
type Claimer interface {
	Claim(ctx context.Context, tenantID, holderID, couponID uint64, orderAmount int64, ref Ref) (int64, string, error)
}

// Engine 参考引擎能力（新项目直接用；自建实现的项目只需满足 Claimer）。
type Engine interface {
	Claimer
	// Issue 向持有者批量发放优惠券（模板启停与发放上限在引擎侧校验）。
	Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error)
	// Coupons 分页查询券实例；holderID=0 表示全部持有者，status 非空时按状态过滤。
	Coupons(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) (CouponPage, error)
	// CreateTemplate 创建优惠券模板（状态固定为启用）。
	CreateTemplate(ctx context.Context, tenantID uint64, t Template) (Template, error)
	// UpdateTemplate 更新模板基础字段与启停状态。
	UpdateTemplate(ctx context.Context, tenantID, id uint64, t Template) error
	// DeleteTemplate 删除模板；已发放过券返回 ErrTemplateHasCoupons。
	DeleteTemplate(ctx context.Context, tenantID, id uint64) error
	// Templates 分页查询模板；status 非空时按状态过滤。
	Templates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) (TemplatePage, error)
}

// Template 优惠券模板视图（参考引擎）。
type Template struct {
	ID          uint64
	Name        string
	Amount      int64  // 面额（分），核销时按面额抵扣
	MinSpend    int64  // 使用门槛（分），0=无门槛
	ValidDays   int    // 发放后有效天数
	TotalCount  int    // 发放上限，0=不限
	IssuedCount int    // 已发放数量
	UsedCount   int    // 已核销数量
	Status      string // enabled / disabled
	Remark      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Coupon 已发放的优惠券实例视图（面额/门槛/有效期随发放快照固化）。
type Coupon struct {
	ID         uint64
	TemplateID uint64
	HolderID   uint64
	Name       string // 券名快照
	Amount     int64  // 面额快照（分）
	MinSpend   int64  // 使用门槛快照（分）
	Status     string // unused / used / expired
	ValidUntil time.Time
	UsedAt     *time.Time // 核销时间；未核销为空
	Ref        Ref
	CreatedAt  time.Time
}

// CouponPage 券实例分页结果。
type CouponPage struct {
	Items []Coupon
	Total int64
	Page  int
	Size  int
}

// TemplatePage 模板分页结果。
type TemplatePage struct {
	Items []Template
	Total int64
	Page  int
	Size  int
}
