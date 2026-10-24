package sessioncard

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/xinpaiyun/nova-lib/member"
)

// Store 定义参考引擎所需的数据访问能力，全部操作限定在租户范围内。
// NewGormStore 提供参考实现；项目也可自建实现后接入 NewEngine。
type Store interface {
	// CreateTemplate 创建次卡模板。
	CreateTemplate(ctx context.Context, t SessionCardTemplate) (SessionCardTemplate, error)
	// FindTemplateByID 在租户范围内查询模板，不存在返回 ErrTemplateNotFound。
	FindTemplateByID(ctx context.Context, tenantID, id uint64) (SessionCardTemplate, error)
	// UpdateTemplate 更新模板字段（引擎侧校验状态与归属）。
	UpdateTemplate(ctx context.Context, tenantID, id uint64, updates map[string]any) error
	// CountCardsByTemplate 统计模板下已发放的卡数量（删除前校验用）。
	CountCardsByTemplate(ctx context.Context, tenantID, templateID uint64) (int64, error)
	// DeleteTemplate 在租户范围内删除模板。
	DeleteTemplate(ctx context.Context, tenantID, id uint64) error
	// ListTemplates 分页查询模板，status 非空时按状态过滤。
	ListTemplates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) ([]SessionCardTemplate, int64, error)
	// Issue 事务发放次卡：校验模板状态与发放上限，为每个持有者生成卡实例。
	Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error)
	// ListCards 分页查询卡实例（含惰性过期）；holderID=0 表示全部持有者。
	ListCards(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) ([]SessionCardModel, int64, error)
	// Consume 事务消费次卡一次：校验归属/状态/有效期/门店后原子扣减次数，
	// 落消费流水。返回剩余次数。
	Consume(ctx context.Context, tenantID, holderID, cardID, shopID uint64, ref Ref) (int, error)
}

// gormStore 基于 GORM 的参考存储（兼容 MySQL 与 SQLite）。
type gormStore struct {
	db *gorm.DB
}

// 编译期断言：参考存储满足引擎数据访问契约。
var _ Store = (*gormStore)(nil)

// NewGormStore 创建基于 GORM 的参考存储；表结构由项目通过 ModelTypes 自行 AutoMigrate。
func NewGormStore(db *gorm.DB) Store {
	return &gormStore{db: db}
}

// ---------- 模板（session_card_templates） ----------

// CreateTemplate 创建次卡模板。
func (s *gormStore) CreateTemplate(ctx context.Context, t SessionCardTemplate) (SessionCardTemplate, error) {
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return SessionCardTemplate{}, err
	}
	return t, nil
}

// FindTemplateByID 在租户范围内查询模板；跨租户访问与不存在同义（404 语义）。
func (s *gormStore) FindTemplateByID(ctx context.Context, tenantID, id uint64) (SessionCardTemplate, error) {
	var template SessionCardTemplate
	err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		First(&template).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionCardTemplate{}, ErrTemplateNotFound
	}
	if err != nil {
		return SessionCardTemplate{}, err
	}
	return template, nil
}

// UpdateTemplate 更新模板字段，未命中返回 ErrTemplateNotFound。
func (s *gormStore) UpdateTemplate(ctx context.Context, tenantID, id uint64, updates map[string]any) error {
	result := s.db.WithContext(ctx).Model(&SessionCardTemplate{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// CountCardsByTemplate 统计模板下已发放的卡数量（删除前校验用）。
func (s *gormStore) CountCardsByTemplate(ctx context.Context, tenantID, templateID uint64) (int64, error) {
	var total int64
	err := s.db.WithContext(ctx).Model(&SessionCardModel{}).
		Where("tenant_id = ? AND template_id = ?", tenantID, templateID).
		Count(&total).Error
	return total, err
}

// DeleteTemplate 在租户范围内删除模板，未命中返回 ErrTemplateNotFound。
func (s *gormStore) DeleteTemplate(ctx context.Context, tenantID, id uint64) error {
	result := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&SessionCardTemplate{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// ListTemplates 分页查询模板列表，支持状态过滤，按 ID 倒序。
func (s *gormStore) ListTemplates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) ([]SessionCardTemplate, int64, error) {
	q = member.NormalizeQuery(q)
	db := s.db.WithContext(ctx).Model(&SessionCardTemplate{}).
		Where("tenant_id = ?", tenantID)
	if status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []SessionCardTemplate
	if err := db.Order("id DESC").
		Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ---------- 发放与消费（session_cards） ----------

// Issue 事务发放次卡：校验模板启停与发放上限，为每个持有者生成卡实例
// （总次数/绑定门店/有效期随发放快照固化），并回写模板已发放数量。
func (s *gormStore) Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error) {
	issued := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var template SessionCardTemplate
		err := tx.Where("id = ? AND tenant_id = ?", templateID, tenantID).First(&template).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTemplateNotFound
		}
		if err != nil {
			return err
		}
		if template.Status != TemplateStatusEnabled {
			return ErrTemplateDisabled
		}
		if template.TotalCount > 0 && template.IssuedCount+len(holderIDs) > template.TotalCount {
			return ErrIssueLimit
		}
		now := time.Now()
		validUntil := now.AddDate(0, 0, template.ValidDays)
		for _, holderID := range holderIDs {
			card := SessionCardModel{
				TenantID:       tenantID,
				TemplateID:     template.ID,
				HolderID:       holderID,
				ShopID:         template.ShopID,
				Name:           template.Name,
				TotalTimes:     template.TotalTimes,
				RemainingTimes: template.TotalTimes,
				ValidUntil:     validUntil,
			}
			if err := tx.Create(&card).Error; err != nil {
				return err
			}
			issued++
		}
		return tx.Model(&SessionCardTemplate{}).
			Where("id = ?", template.ID).
			Update("issued_count", gorm.Expr("issued_count + ?", len(holderIDs))).Error
	})
	if err != nil {
		return 0, err
	}
	return issued, nil
}

// expireStaleCards 惰性过期：把已过有效期仍使用中的卡标记为 expired。
func (s *gormStore) expireStaleCards(ctx context.Context, tenantID uint64) error {
	return s.db.WithContext(ctx).Model(&SessionCardModel{}).
		Where("tenant_id = ? AND status = ? AND valid_until < ?", tenantID, CardActive, time.Now()).
		Update("status", CardExpired).Error
}

// ListCards 分页查询卡实例（查询前先惰性过期），支持持有者/状态过滤，按 ID 倒序。
func (s *gormStore) ListCards(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) ([]SessionCardModel, int64, error) {
	if err := s.expireStaleCards(ctx, tenantID); err != nil {
		return nil, 0, err
	}
	q = member.NormalizeQuery(q)
	db := s.db.WithContext(ctx).Model(&SessionCardModel{}).
		Where("tenant_id = ?", tenantID)
	if holderID > 0 {
		db = db.Where("holder_id = ?", holderID)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []SessionCardModel
	if err := db.Order("id DESC").
		Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Consume 事务消费次卡一次（org-shop-member 契约）：校验归属/状态/有效期/门店后
// 原子扣减剩余次数，次数用尽置 exhausted，并落消费流水（必带 shop_id）。
// 返回剩余次数。
func (s *gormStore) Consume(ctx context.Context, tenantID, holderID, cardID, shopID uint64, ref Ref) (int, error) {
	var remaining int
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var card SessionCardModel
		err := tx.Where("id = ? AND tenant_id = ?", cardID, tenantID).First(&card).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCardNotFound
		}
		if err != nil {
			return err
		}
		if card.HolderID != holderID || card.Status != CardActive {
			return ErrCardNotAvailable
		}
		if card.ValidUntil.Before(time.Now()) {
			return ErrCardNotAvailable
		}
		// 绑店卡仅绑定门店可消费（org-shop-member 契约）：含 shopID=0 的品牌级上下文；
		// 品牌通用卡（ShopID=0）不受限制。
		if card.ShopID > 0 && card.ShopID != shopID {
			return ErrCardShopMismatch
		}
		// 原子扣减次数：remaining_times > 0 才允许扣减，防并发超扣。
		result := tx.Model(&SessionCardModel{}).
			Where("id = ? AND remaining_times > 0", card.ID).
			Updates(map[string]any{
				"remaining_times": gorm.Expr("remaining_times - 1"),
				"used_times":      gorm.Expr("used_times + 1"),
				"updated_at":      time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrCardNotAvailable
		}
		// 扣减后剩余为 0 则置 exhausted。
		if err := tx.Model(&SessionCardModel{}).
			Where("id = ? AND remaining_times = 0 AND status = ?", card.ID, CardActive).
			Update("status", CardExhausted).Error; err != nil {
			return err
		}
		if err := tx.Create(&SessionCardConsumeLog{
			TenantID: tenantID,
			CardID:   card.ID,
			HolderID: holderID,
			ShopID:   shopID,
			RefType:  ref.Type,
			RefID:    ref.ID,
			RefNo:    ref.No,
		}).Error; err != nil {
			return err
		}
		remaining = card.RemainingTimes - 1
		return nil
	})
	if err != nil {
		return 0, err
	}
	return remaining, nil
}
