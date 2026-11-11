package member

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// GormSource 基于标准表 customer_members 实现 CustomerSource 与 CustomerWriter，
// 并把储值账户（member_accounts）的持有者一并维护。
type GormSource struct {
	db *gorm.DB
}

var (
	_ CustomerSource = (*GormSource)(nil)
	_ CustomerWriter = (*GormSource)(nil)
)

// NewGormSource 创建 C 端会员数据源。
func NewGormSource(db *gorm.DB) *GormSource { return &GormSource{db: db} }

// DB 暴露底层连接，供可选 MergeHook 使用。
func (s *GormSource) DB() *gorm.DB { return s.db }

// CreateCustomer 建档：同租户已有会员时幂等返回已有记录；已并入档案明确报错。
func (s *GormSource) CreateCustomer(ctx context.Context, in CustomerInput) (*CustomerMember, error) {
	if in.UserID == 0 {
		return nil, ErrCustomerUserRequired
	}
	if in.TenantID == 0 {
		return nil, ErrCustomerInputInvalid
	}

	var existing CustomerMember
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ?", in.TenantID, in.UserID).
		Take(&existing).Error
	if err == nil {
		if existing.Status == CustomerStatusMerged {
			return nil, ErrCustomerMerged
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = CustomerSourceScanQR
	}
	customer := &CustomerMember{
		TenantID:  in.TenantID,
		OrgID:     in.OrgID,
		UserID:    in.UserID,
		Name:      strings.TrimSpace(in.Name),
		Nickname:  strings.TrimSpace(in.Nickname),
		Phone:     strings.TrimSpace(in.Phone),
		Gender:    strings.TrimSpace(in.Gender),
		Birthday:  strings.TrimSpace(in.Birthday),
		LevelCode: strings.TrimSpace(in.LevelCode),
		Source:    source,
		Tags:      in.Tags,
		Remark:    in.Remark,
		Status:    CustomerStatusEnabled,
	}
	if err := s.db.WithContext(ctx).Create(customer).Error; err != nil {
		// 并发建档撞唯一键时回读已有记录，保证幂等。
		var again CustomerMember
		if e := s.db.WithContext(ctx).
			Where("tenant_id = ? AND user_id = ?", in.TenantID, in.UserID).
			Take(&again).Error; e == nil && again.Status != CustomerStatusMerged {
			return &again, nil
		}
		return nil, err
	}
	return customer, nil
}

// UpdateCustomer 更新会员资料；user_id 与积分不在本接口内改动。
func (s *GormSource) UpdateCustomer(ctx context.Context, tenantID, id uint64, in CustomerInput) error {
	if _, err := s.customerBy(ctx, "tenant_id = ? AND id = ?", tenantID, id); err != nil {
		return err
	}
	updates := map[string]any{
		"name":       strings.TrimSpace(in.Name),
		"nickname":   strings.TrimSpace(in.Nickname),
		"phone":      strings.TrimSpace(in.Phone),
		"gender":     strings.TrimSpace(in.Gender),
		"birthday":   strings.TrimSpace(in.Birthday),
		"level_code": strings.TrimSpace(in.LevelCode),
		"tags":       in.Tags,
		"remark":     in.Remark,
		"updated_at": time.Now(),
	}
	if in.OrgID > 0 {
		updates["org_id"] = in.OrgID
	}
	return s.db.WithContext(ctx).Model(&CustomerMember{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(updates).Error
}

// MergeCustomers 合并会员：业务外键（via hook）、积分、储值账户一并并入目标档案。
//
// 已并入同一目标的历史行会被跳过，因此重复调用是幂等的；已并入其他目标则拒绝。
func (s *GormSource) MergeCustomers(ctx context.Context, tenantID uint64, fromIDs []uint64, intoID uint64, hook MergeHook) (*MergeResult, error) {
	if tenantID == 0 || intoID == 0 || len(fromIDs) == 0 {
		return nil, ErrCustomerMergeInvalid
	}

	result := &MergeResult{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var into CustomerMember
		if err := tx.Where("tenant_id = ? AND id = ? AND status <> ?", tenantID, intoID, CustomerStatusMerged).
			Take(&into).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCustomerNotFound
			}
			return err
		}

		seen := make(map[uint64]struct{}, len(fromIDs))
		for _, fromID := range fromIDs {
			if fromID == 0 {
				continue
			}
			if _, ok := seen[fromID]; ok {
				continue
			}
			seen[fromID] = struct{}{}
			if fromID == intoID {
				return ErrCustomerMergeInvalid
			}

			var from CustomerMember
			if err := tx.Where("tenant_id = ? AND id = ?", tenantID, fromID).Take(&from).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrCustomerNotFound
				}
				return err
			}
			if from.Status == CustomerStatusMerged {
				if from.MergedIntoID == intoID {
					continue
				}
				return ErrCustomerMergeInvalid
			}

			// 项目自有业务表（预约/订单/档案）在同一事务内改写外键。
			if hook != nil {
				if err := hook(tx, tenantID, fromID, intoID); err != nil {
					return err
				}
			}
			if err := mergeAccounts(tx, tenantID, fromID, intoID); err != nil {
				return err
			}
			if err := tx.Model(&CustomerMember{}).
				Where("tenant_id = ? AND id = ?", tenantID, fromID).
				Updates(map[string]any{
					"status":         CustomerStatusMerged,
					"merged_into_id": intoID,
					"updated_at":     time.Now(),
				}).Error; err != nil {
				return err
			}

			into.Points += from.Points
			if into.LegacyID == 0 {
				into.LegacyID = fromID
			}
			result.Merged = append(result.Merged, fromID)
		}

		if err := tx.Model(&CustomerMember{}).
			Where("tenant_id = ? AND id = ?", tenantID, intoID).
			Updates(map[string]any{
				"points":     into.Points,
				"legacy_id":  into.LegacyID,
				"updated_at": time.Now(),
			}).Error; err != nil {
			return err
		}

		var merged CustomerMember
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, intoID).Take(&merged).Error; err != nil {
			return err
		}
		balance, err := accountBalanceTx(tx, tenantID, intoID)
		if err != nil {
			return err
		}
		result.Into = &merged
		result.Balance = balance
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CustomerByUser 按 (tenant_id, user_id) 查询会员，已并入档案不返回。
func (s *GormSource) CustomerByUser(ctx context.Context, tenantID, userID uint64) (*CustomerMember, error) {
	return s.customerBy(ctx, "tenant_id = ? AND user_id = ?", tenantID, userID)
}

// CustomerByID 按主键查询会员，已并入档案不返回。
func (s *GormSource) CustomerByID(ctx context.Context, tenantID, id uint64) (*CustomerMember, error) {
	return s.customerBy(ctx, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *GormSource) customerBy(ctx context.Context, where string, args ...any) (*CustomerMember, error) {
	var customer CustomerMember
	err := s.db.WithContext(ctx).
		Where(where, args...).
		Where("status <> ?", CustomerStatusMerged).
		Take(&customer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCustomerNotFound
	}
	if err != nil {
		return nil, err
	}
	return &customer, nil
}

// Customers 分页查询会员，按 ID 倒序。未指定 Status 时排除已并入档案。
func (s *GormSource) Customers(ctx context.Context, tenantID uint64, filter CustomerFilter) ([]CustomerMember, int64, error) {
	// 用工厂函数构造查询，避免 Count 改写 Statement 后影响后续 Find。
	build := func() *gorm.DB {
		query := s.db.WithContext(ctx).Model(&CustomerMember{}).
			Where("tenant_id = ?", tenantID)
		if filter.Status != nil {
			query = query.Where("status = ?", *filter.Status)
		} else {
			query = query.Where("status <> ?", CustomerStatusMerged)
		}
		if filter.OrgID > 0 {
			query = query.Where("org_id = ?", filter.OrgID)
		}
		if level := strings.TrimSpace(filter.LevelCode); level != "" {
			query = query.Where("level_code = ?", level)
		}
		if kw := strings.TrimSpace(filter.Keyword); kw != "" {
			like := "%" + kw + "%"
			query = query.Where("name LIKE ? OR nickname LIKE ? OR phone LIKE ?", like, like, like)
		}
		return query
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, pageSize := filter.Page, filter.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	var list []CustomerMember
	err := build().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// mergeAccounts 把 fromHolder 的储值账户并入 intoHolder：
// 余额与累计值累加、流水归属改写，旧账户删除。
func mergeAccounts(tx *gorm.DB, tenantID, fromHolder, intoHolder uint64) error {
	var from MemberAccount
	err := tx.Where("tenant_id = ? AND holder_id = ?", tenantID, fromHolder).First(&from).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	into, err := ensureAccount(tx, tenantID, intoHolder)
	if err != nil {
		return err
	}
	if err := tx.Model(&MemberAccount{}).
		Where("id = ? AND tenant_id = ?", into.ID, tenantID).
		Updates(map[string]any{
			"balance":         gorm.Expr("balance + ?", from.Balance),
			"total_recharged": gorm.Expr("total_recharged + ?", from.TotalRecharged),
			"total_spent":     gorm.Expr("total_spent + ?", from.TotalSpent),
			"updated_at":      time.Now(),
		}).Error; err != nil {
		return err
	}
	if err := tx.Model(&MemberTransaction{}).
		Where("tenant_id = ? AND holder_id = ?", tenantID, fromHolder).
		Update("holder_id", intoHolder).Error; err != nil {
		return err
	}
	return tx.Where("id = ? AND tenant_id = ?", from.ID, tenantID).Delete(&MemberAccount{}).Error
}

// accountBalanceTx 查询事务内账户余额，未开户返回 0。
func accountBalanceTx(tx *gorm.DB, tenantID, holderID uint64) (int64, error) {
	var account MemberAccount
	err := tx.Where("tenant_id = ? AND holder_id = ?", tenantID, holderID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return account.Balance, nil
}
