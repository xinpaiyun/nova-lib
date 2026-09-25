package coupon

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
	// CreateTemplate 创建优惠券模板。
	CreateTemplate(ctx context.Context, t CouponTemplate) (CouponTemplate, error)
	// FindTemplateByID 在租户范围内查询模板，不存在返回 ErrTemplateNotFound。
	FindTemplateByID(ctx context.Context, tenantID, id uint64) (CouponTemplate, error)
	// UpdateTemplate 更新模板字段（引擎侧校验状态与归属）。
	UpdateTemplate(ctx context.Context, tenantID, id uint64, updates map[string]any) error
	// CountCouponsByTemplate 统计模板下已发放的券数量（删除前校验用）。
	CountCouponsByTemplate(ctx context.Context, tenantID, templateID uint64) (int64, error)
	// DeleteTemplate 在租户范围内删除模板。
	DeleteTemplate(ctx context.Context, tenantID, id uint64) error
	// ListTemplates 分页查询模板，status 非空时按状态过滤。
	ListTemplates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) ([]CouponTemplate, int64, error)
	// Issue 事务发放优惠券：校验模板状态与发放上限，为每个持有者生成券实例。
	Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error)
	// ListCoupons 分页查询券实例（含惰性过期）；holderID=0 表示全部持有者。
	ListCoupons(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) ([]CouponModel, int64, error)
	// Claim 事务核销优惠券：强校验归属/状态/有效期/门槛后原子占用，
	// 返回核销抵扣金额与券名快照。
	Claim(ctx context.Context, tenantID, holderID, couponID uint64, orderAmount int64, ref Ref) (int64, string, error)
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

// ---------- 模板（coupon_templates） ----------

// CreateTemplate 创建优惠券模板。
func (s *gormStore) CreateTemplate(ctx context.Context, t CouponTemplate) (CouponTemplate, error) {
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return CouponTemplate{}, err
	}
	return t, nil
}

// FindTemplateByID 在租户范围内查询模板；跨租户访问与不存在同义（404 语义）。
func (s *gormStore) FindTemplateByID(ctx context.Context, tenantID, id uint64) (CouponTemplate, error) {
	var template CouponTemplate
	err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		First(&template).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CouponTemplate{}, ErrTemplateNotFound
	}
	if err != nil {
		return CouponTemplate{}, err
	}
	return template, nil
}

// UpdateTemplate 更新模板字段，未命中返回 ErrTemplateNotFound。
func (s *gormStore) UpdateTemplate(ctx context.Context, tenantID, id uint64, updates map[string]any) error {
	result := s.db.WithContext(ctx).Model(&CouponTemplate{}).
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

// CountCouponsByTemplate 统计模板下已发放的券数量（删除前校验用）。
func (s *gormStore) CountCouponsByTemplate(ctx context.Context, tenantID, templateID uint64) (int64, error) {
	var total int64
	err := s.db.WithContext(ctx).Model(&CouponModel{}).
		Where("tenant_id = ? AND template_id = ?", tenantID, templateID).
		Count(&total).Error
	return total, err
}

// DeleteTemplate 在租户范围内删除模板，未命中返回 ErrTemplateNotFound。
func (s *gormStore) DeleteTemplate(ctx context.Context, tenantID, id uint64) error {
	result := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&CouponTemplate{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// ListTemplates 分页查询模板列表，支持状态过滤，按 ID 倒序。
func (s *gormStore) ListTemplates(ctx context.Context, tenantID uint64, status string, q member.PageQuery) ([]CouponTemplate, int64, error) {
	q = member.NormalizeQuery(q)
	db := s.db.WithContext(ctx).Model(&CouponTemplate{}).
		Where("tenant_id = ?", tenantID)
	if status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []CouponTemplate
	if err := db.Order("id DESC").
		Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ---------- 发放与核销（coupons） ----------

// Issue 事务发放优惠券：校验模板启停与发放上限，为每个持有者生成券实例
// （有效期随发放时点计算），并回写模板已发放数量。
func (s *gormStore) Issue(ctx context.Context, tenantID, templateID uint64, holderIDs []uint64) (int, error) {
	issued := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var template CouponTemplate
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
			coupon := CouponModel{
				TenantID:   tenantID,
				TemplateID: template.ID,
				HolderID:   holderID,
				Name:       template.Name,
				Amount:     template.Amount,
				MinSpend:   template.MinSpend,
				ValidUntil: validUntil,
			}
			if err := tx.Create(&coupon).Error; err != nil {
				return err
			}
			issued++
		}
		return tx.Model(&CouponTemplate{}).
			Where("id = ?", template.ID).
			Update("issued_count", gorm.Expr("issued_count + ?", len(holderIDs))).Error
	})
	if err != nil {
		return 0, err
	}
	return issued, nil
}

// expireStaleCoupons 惰性过期：把已过有效期仍未使用的券标记为 expired。
func (s *gormStore) expireStaleCoupons(ctx context.Context, tenantID uint64) error {
	return s.db.WithContext(ctx).Model(&CouponModel{}).
		Where("tenant_id = ? AND status = ? AND valid_until < ?", tenantID, CouponUnused, time.Now()).
		Update("status", CouponExpired).Error
}

// ListCoupons 分页查询券实例（查询前先惰性过期），支持持有者/状态过滤，按 ID 倒序。
func (s *gormStore) ListCoupons(ctx context.Context, tenantID, holderID uint64, status string, q member.PageQuery) ([]CouponModel, int64, error) {
	if err := s.expireStaleCoupons(ctx, tenantID); err != nil {
		return nil, 0, err
	}
	q = member.NormalizeQuery(q)
	db := s.db.WithContext(ctx).Model(&CouponModel{}).
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
	var rows []CouponModel
	if err := db.Order("id DESC").
		Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Claim 事务核销优惠券（结算抵扣）：校验归属/状态/有效期/门槛后原子占用，
// 落关联单据并回写模板核销计数。返回核销抵扣金额与券名快照。
func (s *gormStore) Claim(ctx context.Context, tenantID, holderID, couponID uint64, orderAmount int64, ref Ref) (int64, string, error) {
	var amount int64
	var name string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var coupon CouponModel
		err := tx.Where("id = ? AND tenant_id = ?", couponID, tenantID).First(&coupon).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCouponNotFound
		}
		if err != nil {
			return err
		}
		if coupon.HolderID != holderID ||
			coupon.Status != CouponUnused ||
			coupon.ValidUntil.Before(time.Now()) ||
			coupon.MinSpend > orderAmount {
			return ErrCouponNotAvailable
		}
		now := time.Now()
		result := tx.Model(&CouponModel{}).
			Where("id = ? AND status = ?", coupon.ID, CouponUnused).
			Updates(map[string]any{
				"status":     CouponUsed,
				"used_at":    &now,
				"ref_type":   ref.Type,
				"ref_id":     ref.ID,
				"ref_no":     ref.No,
				"updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrCouponNotAvailable
		}
		if err := tx.Model(&CouponTemplate{}).
			Where("id = ?", coupon.TemplateID).
			Update("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
			return err
		}
		amount = coupon.Amount
		name = coupon.Name
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	return amount, name, nil
}
