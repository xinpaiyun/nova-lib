// Package catalog 提供商家可售卖项目（服务项目/价格字典）的通用引擎：
// 名称 + 分类 + 售价 +（可选）标准时长 + 上下架状态，供开单/下单时引用做价格快照。
// 与 member/coupon/plan 同为"契约 + 参考引擎"模式；分类取值由项目定义（如洗车/美容、
// 洗护/寄养），引擎不感知具体业务编码。
package catalog

import (
	"context"
	"errors"
	"time"
)

// 项目状态。
const (
	StatusEnabled  = "enabled"  // 启用（可开单计价）
	StatusDisabled = "disabled" // 停用
)

// 套餐项目错误。
var (
	ErrItemNotFound = errors.New("可售卖项目不存在")
	ErrInvalidItem  = errors.New("项目信息不完整或非法")
)

// Item 定义可售卖项目视图。
type Item struct {
	ID       uint64 `json:"id"`
	TenantID uint64 `json:"tenantId"`
	// ShopID 归属经营点（0 = 租户级项目，多经营点共享）。
	ShopID uint64 `json:"shopId"`
	Name   string `json:"name"`
	// Category 分类编码，取值由项目定义。
	Category string `json:"category"`
	// Price 标准售价（分）。
	Price int64 `json:"price"`
	// DurationMinutes 标准时长（分钟），0 = 未填；非服务类项目可忽略。
	DurationMinutes int       `json:"durationMinutes"`
	Status          string    `json:"status"`
	Remark          string    `json:"remark,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// ItemInput 定义创建/更新入参（校验规则同引擎实现）。
type ItemInput struct {
	ShopID          uint64 `json:"shopId"`
	Name            string `json:"name"`
	Category        string `json:"category"`
	Price           int64  `json:"price"`
	DurationMinutes int    `json:"durationMinutes"`
	Status          string `json:"status"`
	Remark          string `json:"remark"`
}

// Query 定义项目列表查询参数；字段为零值时不参与过滤。
type Query struct {
	ShopID   uint64 `json:"shopId"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Keyword  string `json:"keyword"` // 名称模糊匹配
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}

// Page 定义项目分页结果。
type Page struct {
	Items []Item `json:"items"`
	Total int64  `json:"total"`
	Page  int    `json:"page"`
	Size  int    `json:"size"`
}

// Engine 可售卖项目引擎契约。
type Engine interface {
	// Create 创建项目；名称非空、价格非负、状态合法。
	Create(ctx context.Context, tenantID uint64, in ItemInput) (Item, error)
	// Update 更新项目（整体替换可编辑字段）。
	Update(ctx context.Context, tenantID, itemID uint64, in ItemInput) error
	// Delete 删除项目；引用快照由项目侧在单据中自行保存，引擎不做引用校验。
	Delete(ctx context.Context, tenantID, itemID uint64) error
	// Item 按 ID 查询。
	Item(ctx context.Context, tenantID, itemID uint64) (Item, error)
	// Items 分页查询项目列表。
	Items(ctx context.Context, tenantID uint64, q Query) (Page, error)
}
