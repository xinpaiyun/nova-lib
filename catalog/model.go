package catalog

import "time"

// ModelTypes 返回可售卖项目模块需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&ItemModel{}}
}

// ItemModel 定义可售卖项目模型（表 catalog_items；租户内名称可重复，靠 ID 引用）。
type ItemModel struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键；ShopID 归属经营点（0 = 租户级）。
	TenantID uint64 `json:"tenantId" gorm:"not null;index:idx_catalog_items_scope,priority:1"`
	ShopID   uint64 `json:"shopId" gorm:"not null;default:0;index:idx_catalog_items_scope,priority:2"`
	Name     string `json:"name" gorm:"not null;size:80"`
	// Category 分类编码（项目定义）；Price 标准售价（分）。
	Category string `json:"category" gorm:"not null;size:32;index"`
	Price    int64  `json:"price" gorm:"not null;default:0"`
	// DurationMinutes 标准时长（分钟，0 = 未填）。
	DurationMinutes int       `json:"durationMinutes" gorm:"not null;default:0"`
	Status          string    `json:"status" gorm:"not null;size:16;default:enabled;index"`
	Remark          string    `json:"remark" gorm:"size:500"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// TableName 返回可售卖项目表名。
func (ItemModel) TableName() string { return "catalog_items" }
