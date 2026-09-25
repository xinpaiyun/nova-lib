package coupon

import (
	"context"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/member"
)

// 模板与发放的业务规则上限。
const (
	maxTemplateAmount = 10_000_000 // 模板面额上限：10 万元（分）
	maxValidDays      = 3650       // 有效天数上限
	maxIssueBatch     = 100        // 单次发放持有者数量上限
)

// engine Engine 契约的参考实现：业务规则（面额上限、发放批量等）在引擎侧校验，数据访问委托给 Store。
type engine struct {
	store Store
}

// 编译期断言：参考引擎满足 Engine 契约。
var _ Engine = (*engine)(nil)

// NewEngine 创建优惠券参考引擎。
// store 可为 NewGormStore 返回的参考实现，也可为项目自建的数据访问实现。
func NewEngine(store Store) Engine {
	return &engine{store: store}
}

// Claim 结算核销优惠券：强校验归属/状态/有效期/门槛后原子占用，返回抵扣金额与券名快照。
func (e *engine) Claim(ctx context.Context, tenantID, holderID, couponID uint64, orderAmount int64, ref Ref) (int64, string, error) {
	return e.store.Claim(ctx, tenantID, holderID, couponID, orderAmount, ref)
}

// Issue 向持有者批量发放优惠券：去重并剔除非法 ID 后交由存储事务发放。
func (e *engine) Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error) {
	seen := make(map[uint64]bool, len(holderIDs))
	ids := make([]uint64, 0, len(holderIDs))
	for _, id := range holderIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, ErrEmptyHolders
	}
	if len(ids) > maxIssueBatch {
		return 0, ErrTooManyHolders
	}
	return e.store.Issue(ctx, tenantID, templateID, ids)
}

// Coupons 分页查询券实例（含惰性过期）；holderID=0 表示全部持有者。
func (e *engine) Coupons(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) (CouponPage, error) {
	q = member.NormalizeQuery(q)
	rows, total, err := e.store.ListCoupons(ctx, tenantID, holderID, status, q)
	if err != nil {
		return CouponPage{}, err
	}
	items := make([]Coupon, 0, len(rows))
	for _, row := range rows {
		items = append(items, couponOf(row))
	}
	return CouponPage{Items: items, Total: total, Page: q.Page, Size: q.PageSize}, nil
}

// CreateTemplate 创建优惠券模板（状态固定为启用）。
func (e *engine) CreateTemplate(ctx context.Context, tenantID uint64, t Template) (Template, error) {
	template, err := buildTemplate(t)
	if err != nil {
		return Template{}, err
	}
	template.TenantID = tenantID
	template.Status = TemplateStatusEnabled
	created, err := e.store.CreateTemplate(ctx, template)
	if err != nil {
		return Template{}, err
	}
	return templateOf(created), nil
}

// UpdateTemplate 更新优惠券模板（基础字段 + 启停状态）。
func (e *engine) UpdateTemplate(ctx context.Context, tenantID, id uint64, t Template) error {
	if _, err := e.store.FindTemplateByID(ctx, tenantID, id); err != nil {
		return err
	}
	template, err := buildTemplate(t)
	if err != nil {
		return err
	}
	if t.Status != TemplateStatusEnabled && t.Status != TemplateStatusDisabled {
		return ErrInvalidStatus
	}
	updates := map[string]any{
		"name":        template.Name,
		"amount":      template.Amount,
		"min_spend":   template.MinSpend,
		"valid_days":  template.ValidDays,
		"total_count": template.TotalCount,
		"status":      t.Status,
		"remark":      template.Remark,
		"updated_at":  time.Now(),
	}
	return e.store.UpdateTemplate(ctx, tenantID, id, updates)
}

// DeleteTemplate 删除模板；已发放过券返回 ErrTemplateHasCoupons。
func (e *engine) DeleteTemplate(ctx context.Context, tenantID, id uint64) error {
	if _, err := e.store.FindTemplateByID(ctx, tenantID, id); err != nil {
		return err
	}
	count, err := e.store.CountCouponsByTemplate(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrTemplateHasCoupons
	}
	return e.store.DeleteTemplate(ctx, tenantID, id)
}

// Templates 分页查询模板；status 非空时按状态过滤。
func (e *engine) Templates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) (TemplatePage, error) {
	q = member.NormalizeQuery(q)
	rows, total, err := e.store.ListTemplates(ctx, tenantID, status, q)
	if err != nil {
		return TemplatePage{}, err
	}
	items := make([]Template, 0, len(rows))
	for _, row := range rows {
		items = append(items, templateOf(row))
	}
	return TemplatePage{Items: items, Total: total, Page: q.Page, Size: q.PageSize}, nil
}

// buildTemplate 校验并构造模板存储对象（统计字段与状态由引擎控制，忽略入参同名值）。
func buildTemplate(t Template) (CouponTemplate, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return CouponTemplate{}, ErrEmptyName
	}
	if t.Amount <= 0 || t.Amount > maxTemplateAmount {
		return CouponTemplate{}, ErrInvalidAmount
	}
	if t.MinSpend < 0 {
		return CouponTemplate{}, ErrInvalidMinSpend
	}
	if t.ValidDays < 1 || t.ValidDays > maxValidDays {
		return CouponTemplate{}, ErrInvalidValidDays
	}
	if t.TotalCount < 0 {
		return CouponTemplate{}, ErrInvalidTotalCount
	}
	return CouponTemplate{
		Name:       name,
		Amount:     t.Amount,
		MinSpend:   t.MinSpend,
		ValidDays:  t.ValidDays,
		TotalCount: t.TotalCount,
		Remark:     strings.TrimSpace(t.Remark),
	}, nil
}

// templateOf 将模板模型转换为契约视图。
func templateOf(t CouponTemplate) Template {
	return Template{
		ID:          t.ID,
		Name:        t.Name,
		Amount:      t.Amount,
		MinSpend:    t.MinSpend,
		ValidDays:   t.ValidDays,
		TotalCount:  t.TotalCount,
		IssuedCount: t.IssuedCount,
		UsedCount:   t.UsedCount,
		Status:      t.Status,
		Remark:      t.Remark,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

// couponOf 将券实例模型转换为契约视图。
func couponOf(c CouponModel) Coupon {
	return Coupon{
		ID:         c.ID,
		TemplateID: c.TemplateID,
		HolderID:   c.HolderID,
		Name:       c.Name,
		Amount:     c.Amount,
		MinSpend:   c.MinSpend,
		Status:     c.Status,
		ValidUntil: c.ValidUntil,
		UsedAt:     c.UsedAt,
		Ref:        Ref{Type: c.RefType, ID: c.RefID, No: c.RefNo},
		CreatedAt:  c.CreatedAt,
	}
}
