// Package sessioncard 提供次卡中台契约与参考引擎：模板管理、批量发放与门店消费核销。
// 次卡为门店级营销能力（org-shop-member 契约）：卡可绑定门店（仅该店可消费）或
// 品牌通用（绑店可选），消费流水必带 shop_id（品牌级上下文为 0）。
// 数据隔离键统一为 TenantID（列 tenant_id），持有者统一为 HolderID
// （C 端用户或客户 ID，语义由项目决定），次数一律为 int。
package sessioncard

import (
	"context"
	"errors"
	"time"

	"github.com/xinpaiyun/nova-lib/member"
)

// Ref 关联业务单据，复用会员包定义。
type Ref = member.Ref

// 次卡错误。
var (
	ErrTemplateNotFound = errors.New("次卡模板不存在")
	ErrTemplateHasCards = errors.New("模板已发放次卡，不能删除")
	ErrTemplateDisabled = errors.New("模板已停用，不能发放")
	ErrIssueLimit       = errors.New("发放数量超出模板剩余可发数量")
	ErrCardNotFound     = errors.New("次卡不存在")
	ErrCardNotAvailable = errors.New("次卡不可用")
	// ErrCardShopMismatch 绑店卡消费门店不匹配（含品牌级上下文 shopID=0 消费绑店卡）。
	ErrCardShopMismatch = errors.New("绑定门店的次卡仅限该门店消费")
)

// 模板与发放的入参校验错误。
var (
	ErrEmptyName         = errors.New("次卡名称不能为空")
	ErrInvalidTotalTimes = errors.New("总次数需在 1-1000 之间")
	ErrInvalidValidDays  = errors.New("有效天数需在 1-3650 之间")
	ErrInvalidTotalCount = errors.New("发放上限不正确")
	ErrInvalidStatus     = errors.New("模板状态不正确")
	ErrEmptyHolders      = errors.New("请选择要发放的持有者")
	ErrTooManyHolders    = errors.New("单次发放数量超出上限")
)

// Engine 次卡引擎能力：模板管理、批量发放与门店消费核销（参考实现 NewEngine）。
type Engine interface {
	// Consume 在门店上下文消费次卡一次（org-shop-member 契约）：校验归属/状态/有效期/门店后
	// 原子扣减剩余次数并落消费流水，返回剩余次数；绑店卡仅绑定门店可消费。
	Consume(ctx context.Context, tenantID, holderID, cardID, shopID uint64, ref Ref) (int, error)
	// Issue 向持有者批量发放次卡（模板启停与发放上限在引擎侧校验）。
	Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error)
	// Cards 分页查询卡实例；holderID=0 表示全部持有者，status 非空时按状态过滤。
	Cards(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) (CardPage, error)
	// CreateTemplate 创建次卡模板（状态固定为启用）。
	CreateTemplate(ctx context.Context, tenantID uint64, t Template) (Template, error)
	// UpdateTemplate 更新次卡模板基础字段与启停状态。
	UpdateTemplate(ctx context.Context, tenantID, id uint64, t Template) error
	// DeleteTemplate 删除模板；已发放过卡返回 ErrTemplateHasCards。
	DeleteTemplate(ctx context.Context, tenantID, id uint64) error
	// Templates 分页查询模板；status 非空时按状态过滤。
	Templates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) (TemplatePage, error)
}

// Template 次卡模板视图（参考引擎）。
type Template struct {
	ID          uint64
	ShopID      uint64 // 绑定门店；0=品牌通用（org-shop-member 契约绑店可选）
	Name        string
	TotalTimes  int // 单卡总次数
	ValidDays   int // 发放后有效天数
	TotalCount  int // 发放上限，0=不限
	IssuedCount int // 已发放数量
	UsedTimes   int // 已核销次数统计
	Status      string // enabled / disabled
	Remark      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Card 已发放的次卡实例视图（次数/有效期/绑店随发放快照固化）。
type Card struct {
	ID             uint64
	TemplateID     uint64
	HolderID       uint64
	ShopID         uint64 // 绑定门店快照；0=品牌通用
	Name           string // 卡名快照
	TotalTimes     int    // 总次数快照
	RemainingTimes int    // 剩余次数
	UsedTimes      int    // 已核销次数
	Status         string // active / exhausted / expired
	ValidUntil     time.Time
	CreatedAt      time.Time
}

// CardPage 卡实例分页结果。
type CardPage struct {
	Items []Card
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
