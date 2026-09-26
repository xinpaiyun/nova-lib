package catalog

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// engine Engine 契约的参考实现：直接持有 GORM 连接。
type engine struct {
	db *gorm.DB
}

// 编译期断言：参考引擎满足 Engine 契约。
var _ Engine = (*engine)(nil)

// NewEngine 创建可售卖项目参考引擎。
func NewEngine(db *gorm.DB) Engine { return &engine{db: db} }

// Create 创建项目。
func (e *engine) Create(ctx context.Context, tenantID uint64, in ItemInput) (Item, error) {
	if tenantID == 0 {
		return Item{}, ErrInvalidItem
	}
	row, err := itemModelFromInput(in, func(m *ItemModel) { m.TenantID = tenantID })
	if err != nil {
		return Item{}, err
	}
	if err := e.db.WithContext(ctx).Create(row).Error; err != nil {
		return Item{}, err
	}
	return itemOf(*row), nil
}

// Update 更新项目。
func (e *engine) Update(ctx context.Context, tenantID, itemID uint64, in ItemInput) error {
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row ItemModel
		if err := tx.Take(&row, "id = ? AND tenant_id = ?", itemID, tenantID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrItemNotFound
			}
			return err
		}
		updates, err := itemUpdatesFromInput(in)
		if err != nil {
			return err
		}
		return tx.Model(&ItemModel{}).Where("id = ?", row.ID).Updates(updates).Error
	})
}

// Delete 删除项目。
func (e *engine) Delete(ctx context.Context, tenantID, itemID uint64) error {
	result := e.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", itemID, tenantID).
		Delete(&ItemModel{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrItemNotFound
	}
	return nil
}

// Item 按 ID 查询。
func (e *engine) Item(ctx context.Context, tenantID, itemID uint64) (Item, error) {
	var row ItemModel
	if err := e.db.WithContext(ctx).Take(&row, "id = ? AND tenant_id = ?", itemID, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Item{}, ErrItemNotFound
		}
		return Item{}, err
	}
	return itemOf(row), nil
}

// Items 分页查询项目列表。
func (e *engine) Items(ctx context.Context, tenantID uint64, q Query) (Page, error) {
	page, size := normalizePage(q.Page, q.PageSize)
	query := e.db.WithContext(ctx).Model(&ItemModel{}).Where("tenant_id = ?", tenantID)
	if q.ShopID > 0 {
		query = query.Where("shop_id = ?", q.ShopID)
	}
	if category := strings.TrimSpace(q.Category); category != "" {
		query = query.Where("category = ?", category)
	}
	if status := strings.TrimSpace(q.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if keyword := strings.TrimSpace(q.Keyword); keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return Page{}, err
	}
	var rows []ItemModel
	if err := query.Order("id ASC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return Page{}, err
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, itemOf(row))
	}
	return Page{Items: items, Total: total, Page: page, Size: size}, nil
}

// validateInput 校验入参并归一化。
func validateInput(in ItemInput) (ItemInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(in.Category)
	in.Status = strings.TrimSpace(in.Status)
	in.Remark = strings.TrimSpace(in.Remark)
	if in.Name == "" || in.Category == "" || in.Price < 0 || in.DurationMinutes < 0 {
		return in, ErrInvalidItem
	}
	if in.Status == "" {
		in.Status = StatusEnabled
	}
	if in.Status != StatusEnabled && in.Status != StatusDisabled {
		return in, ErrInvalidItem
	}
	return in, nil
}

// itemModelFromInput 由入参构建新项目模型（mutate 用于补充租户等创建期字段）。
func itemModelFromInput(in ItemInput, mutate func(*ItemModel)) (*ItemModel, error) {
	in, err := validateInput(in)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	row := &ItemModel{
		ShopID:          in.ShopID,
		Name:            in.Name,
		Category:        in.Category,
		Price:           in.Price,
		DurationMinutes: in.DurationMinutes,
		Status:          in.Status,
		Remark:          in.Remark,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if mutate != nil {
		mutate(row)
	}
	return row, nil
}

// itemUpdatesFromInput 由入参构建更新字段。
func itemUpdatesFromInput(in ItemInput) (map[string]any, error) {
	in, err := validateInput(in)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"shop_id":          in.ShopID,
		"name":             in.Name,
		"category":         in.Category,
		"price":            in.Price,
		"duration_minutes": in.DurationMinutes,
		"status":           in.Status,
		"remark":           in.Remark,
		"updated_at":       time.Now(),
	}, nil
}

func itemOf(row ItemModel) Item {
	return Item{
		ID:              row.ID,
		TenantID:        row.TenantID,
		ShopID:          row.ShopID,
		Name:            row.Name,
		Category:        row.Category,
		Price:           row.Price,
		DurationMinutes: row.DurationMinutes,
		Status:          row.Status,
		Remark:          row.Remark,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
