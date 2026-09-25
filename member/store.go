package member

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Store 定义参考引擎所需的数据访问能力，全部操作限定在租户范围内。
// NewGormStore 提供参考实现；项目也可自建实现后接入 NewEngine。
type Store interface {
	// Recharge 事务充值：无账户自动开户，入账并落充值流水，返回变动后的账户。
	Recharge(ctx context.Context, tenantID, holderID uint64, amount int64, ref Ref, remark string) (MemberAccount, error)
	// Deduct 事务扣减余额：实扣不超过现有余额并落消费流水，返回实扣金额
	// （账户不存在或余额为 0 时返回 0）。
	Deduct(ctx context.Context, tenantID, holderID uint64, amount int64, ref Ref) (int64, error)
	// FindAccount 查询账户，不存在返回 ErrAccountNotFound。
	FindAccount(ctx context.Context, tenantID, holderID uint64) (MemberAccount, error)
	// AccountBalance 查询余额，未开户返回 0。
	AccountBalance(ctx context.Context, tenantID, holderID uint64) (int64, error)
	// ListAccounts 分页查询账户（不联查持有者资料）。
	ListAccounts(ctx context.Context, tenantID uint64, q PageQuery) ([]MemberAccount, int64, error)
	// ListTransactions 分页查询流水，q.Type 非空时按类型过滤。
	ListTransactions(ctx context.Context, tenantID, holderID uint64, q PageQuery) ([]MemberTransaction, int64, error)
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

// normalizeError 归一化 gorm 错误。
func normalizeError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAccountNotFound
	}
	return err
}

// ensureAccount 在事务内取出会员账户，不存在则开户（余额 0）。
func ensureAccount(tx *gorm.DB, tenantID, holderID uint64) (MemberAccount, error) {
	var account MemberAccount
	err := tx.Where("tenant_id = ? AND holder_id = ?", tenantID, holderID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		account = MemberAccount{TenantID: tenantID, HolderID: holderID}
		if err := tx.Create(&account).Error; err != nil {
			return MemberAccount{}, err
		}
		return account, nil
	}
	if err != nil {
		return MemberAccount{}, err
	}
	return account, nil
}

// Recharge 事务充值：无账户自动开户，余额与累计充值入账并落充值流水。
func (s *gormStore) Recharge(ctx context.Context, tenantID, holderID uint64, amount int64, ref Ref, remark string) (MemberAccount, error) {
	var account MemberAccount
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := ensureAccount(tx, tenantID, holderID)
		if err != nil {
			return err
		}
		if err := tx.Model(&MemberAccount{}).
			Where("id = ? AND tenant_id = ?", current.ID, tenantID).
			Updates(map[string]any{
				"balance":         gorm.Expr("balance + ?", amount),
				"total_recharged": gorm.Expr("total_recharged + ?", amount),
				"updated_at":      time.Now(),
			}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ? AND tenant_id = ?", current.ID, tenantID).First(&account).Error; err != nil {
			return err
		}
		txn := MemberTransaction{
			TenantID:     tenantID,
			HolderID:     holderID,
			Type:         TypeRecharge,
			Amount:       amount,
			BalanceAfter: account.Balance,
			RefType:      ref.Type,
			RefID:        ref.ID,
			RefNo:        ref.No,
			Remark:       remark,
		}
		return tx.Create(&txn).Error
	})
	if err != nil {
		return MemberAccount{}, err
	}
	return account, nil
}

// Deduct 事务扣减余额（消费抵扣）：扣减金额不超过现有余额，落消费流水并关联业务单据。
// 返回实际扣减金额；账户不存在或余额为 0 时返回 0。
func (s *gormStore) Deduct(ctx context.Context, tenantID, holderID uint64, amount int64, ref Ref) (int64, error) {
	var deducted int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		account, err := ensureAccount(tx, tenantID, holderID)
		if err != nil {
			return err
		}
		deducted = account.Balance
		if deducted > amount {
			deducted = amount
		}
		if deducted <= 0 {
			return nil
		}
		result := tx.Model(&MemberAccount{}).
			Where("id = ? AND tenant_id = ? AND balance >= ?", account.ID, tenantID, deducted).
			Updates(map[string]any{
				"balance":     gorm.Expr("balance - ?", deducted),
				"total_spent": gorm.Expr("total_spent + ?", deducted),
				"updated_at":  time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrInsufficientBalance
		}
		var after MemberAccount
		if err := tx.Where("id = ? AND tenant_id = ?", account.ID, tenantID).First(&after).Error; err != nil {
			return err
		}
		txn := MemberTransaction{
			TenantID:     tenantID,
			HolderID:     holderID,
			Type:         TypeSpend,
			Amount:       deducted,
			BalanceAfter: after.Balance,
			RefType:      ref.Type,
			RefID:        ref.ID,
			RefNo:        ref.No,
		}
		return tx.Create(&txn).Error
	})
	if err != nil {
		return 0, err
	}
	return deducted, nil
}

// FindAccount 查询会员账户；不存在返回 ErrAccountNotFound（404 语义）。
func (s *gormStore) FindAccount(ctx context.Context, tenantID, holderID uint64) (MemberAccount, error) {
	var account MemberAccount
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND holder_id = ?", tenantID, holderID).
		First(&account).Error; err != nil {
		return MemberAccount{}, normalizeError(err)
	}
	return account, nil
}

// AccountBalance 查询会员余额；账户不存在返回 0。
func (s *gormStore) AccountBalance(ctx context.Context, tenantID, holderID uint64) (int64, error) {
	var account MemberAccount
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND holder_id = ?", tenantID, holderID).
		First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return account.Balance, nil
}

// ListAccounts 分页查询会员账户（不联查持有者资料），按 ID 倒序。
func (s *gormStore) ListAccounts(ctx context.Context, tenantID uint64, q PageQuery) ([]MemberAccount, int64, error) {
	q = NormalizeQuery(q)
	db := s.db.WithContext(ctx).Model(&MemberAccount{}).
		Where("tenant_id = ?", tenantID)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MemberAccount
	if err := db.Order("id DESC").
		Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListTransactions 分页查询会员资金流水，支持类型过滤，按 ID 倒序。
func (s *gormStore) ListTransactions(ctx context.Context, tenantID, holderID uint64, q PageQuery) ([]MemberTransaction, int64, error) {
	q = NormalizeQuery(q)
	db := s.db.WithContext(ctx).Model(&MemberTransaction{}).
		Where("tenant_id = ? AND holder_id = ?", tenantID, holderID)
	if q.Type != "" {
		db = db.Where("type = ?", q.Type)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MemberTransaction
	if err := db.Order("id DESC").
		Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
