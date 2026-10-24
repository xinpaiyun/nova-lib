package sessioncard

import (
	"context"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/member"
)

// 模板与发放的业务规则上限。
const (
	maxTotalTimes = 1000 // 单卡次数上限
	maxValidDays  = 3650 // 有效天数上限
	maxIssueBatch = 100  // 单次发放持有者数量上限
)

// engine Engine 契约的参考实现：业务规则（次数上限、发放批量等）在引擎侧校验，数据访问委托给 Store。
type engine struct {
	store Store
}

// 编译期断言：参考引擎满足 Engine 契约。
var _ Engine = (*engine)(nil)

// NewEngine 创建次卡参考引擎。
// store 可为 NewGormStore 返回的参考实现，也可为项目自建的数据访问实现。
func NewEngine(store Store) Engine {
	return &engine{store: store}
}

// Consume 在门店上下文消费次卡一次：强校验归属/状态/有效期/门店后原子扣减，返回剩余次数。
func (e *engine) Consume(ctx context.Context, tenantID, holderID, cardID, shopID uint64, ref Ref) (int, error) {
	return e.store.Consume(ctx, tenantID, holderID, cardID, shopID, ref)
}

// Issue 向持有者批量发放次卡：去重并剔除非法 ID 后交由存储事务发放。
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

// Cards 分页查询卡实例（含惰性过期）；holderID=0 表示全部持有者。
func (e *engine) Cards(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) (CardPage, error) {
	q = member.NormalizeQuery(q)
	rows, total, err := e.store.ListCards(ctx, tenantID, holderID, status, q)
	if err != nil {
		return CardPage{}, err
	}
	items := make([]Card, 0, len(rows))
	for _, row := range rows {
		items = append(items, cardOf(row))
	}
	return CardPage{Items: items, Total: total, Page: q.Page, Size: q.PageSize}, nil
}

// CreateTemplate 创建次卡模板（状态固定为启用）。
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

// UpdateTemplate 更新次卡模板（基础字段 + 启停状态）。
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
		"shop_id":     template.ShopID,
		"total_times": template.TotalTimes,
		"valid_days":  template.ValidDays,
		"total_count": template.TotalCount,
		"status":      t.Status,
		"remark":      template.Remark,
		"updated_at":  time.Now(),
	}
	return e.store.UpdateTemplate(ctx, tenantID, id, updates)
}

// DeleteTemplate 删除模板；已发放过卡返回 ErrTemplateHasCards。
func (e *engine) DeleteTemplate(ctx context.Context, tenantID, id uint64) error {
	if _, err := e.store.FindTemplateByID(ctx, tenantID, id); err != nil {
		return err
	}
	count, err := e.store.CountCardsByTemplate(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrTemplateHasCards
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
// 绑店可选（org-shop-member 契约）：ShopID=0 为品牌通用，>0 仅该店可消费。
func buildTemplate(t Template) (SessionCardTemplate, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return SessionCardTemplate{}, ErrEmptyName
	}
	if t.TotalTimes < 1 || t.TotalTimes > maxTotalTimes {
		return SessionCardTemplate{}, ErrInvalidTotalTimes
	}
	if t.ValidDays < 1 || t.ValidDays > maxValidDays {
		return SessionCardTemplate{}, ErrInvalidValidDays
	}
	if t.TotalCount < 0 {
		return SessionCardTemplate{}, ErrInvalidTotalCount
	}
	return SessionCardTemplate{
		Name:       name,
		ShopID:     t.ShopID,
		TotalTimes: t.TotalTimes,
		ValidDays:  t.ValidDays,
		TotalCount: t.TotalCount,
		Remark:     strings.TrimSpace(t.Remark),
	}, nil
}

// templateOf 将模板模型转换为契约视图。
func templateOf(t SessionCardTemplate) Template {
	return Template{
		ID:          t.ID,
		ShopID:      t.ShopID,
		Name:        t.Name,
		TotalTimes:  t.TotalTimes,
		ValidDays:   t.ValidDays,
		TotalCount:  t.TotalCount,
		IssuedCount: t.IssuedCount,
		UsedTimes:   t.UsedTimes,
		Status:      t.Status,
		Remark:      t.Remark,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

// cardOf 将卡实例模型转换为契约视图。
func cardOf(c SessionCardModel) Card {
	return Card{
		ID:             c.ID,
		TemplateID:     c.TemplateID,
		HolderID:       c.HolderID,
		ShopID:         c.ShopID,
		Name:           c.Name,
		TotalTimes:     c.TotalTimes,
		RemainingTimes: c.RemainingTimes,
		UsedTimes:      c.UsedTimes,
		Status:         c.Status,
		ValidUntil:     c.ValidUntil,
		CreatedAt:      c.CreatedAt,
	}
}
