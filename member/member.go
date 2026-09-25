// Package member 提供会员储值中台契约与参考引擎。
// 契约（Wallet）供各项目实现自建存储（如已有自建实现的项目适配契约），
// 参考引擎（NewGormStore + NewEngine）供新项目直接复用。
// 数据隔离键统一为 TenantID（列 tenant_id），持有者统一为 HolderID
// （C 端用户或客户 ID，语义由项目决定），关联单据统一为 Ref{Type, ID, No}，
// 金额一律为分（int64）。
package member

import (
	"context"
	"errors"
	"time"
)

// Ref 关联业务单据：Type 为业务单据类型（work_order / service_order / recharge / admin 等），
// ID 为单据 ID，No 为可选单号。
type Ref struct {
	Type string
	ID   uint64
	No   string // 可选单号
}

// Account 会员储值账户视图。
type Account struct {
	TenantID       uint64
	HolderID       uint64
	Balance        int64 // 余额（分）
	TotalRecharged int64 // 累计充值（分）
	TotalSpent     int64 // 累计消费抵扣（分）
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Transaction 会员资金流水视图（只增不改，余额变动必留痕）。
type Transaction struct {
	ID           uint64
	Type         string // recharge / spend
	Amount       int64  // 变动金额（分），恒为正
	BalanceAfter int64  // 变动后余额（分）
	Ref          Ref
	Remark       string
	CreatedAt    time.Time
}

// PageQuery 分页查询参数；Type 仅流水过滤用。
type PageQuery struct {
	Page     int
	PageSize int
	Type     string
}

// AccountPage 会员账户分页结果。
type AccountPage struct {
	Items []Account
	Total int64
	Page  int
	Size  int
}

// TransactionPage 会员流水分页结果。
type TransactionPage struct {
	Items []Transaction
	Total int64
	Page  int
	Size  int
}

// 会员储值错误。
var (
	ErrInvalidAmount       = errors.New("金额不正确")
	ErrAccountNotFound     = errors.New("会员账户不存在")
	ErrInsufficientBalance = errors.New("会员余额不足")
)

// Wallet 会员储值中台契约：项目可实现该接口（自建存储），或直接使用参考引擎。
type Wallet interface {
	// Recharge 会员充值：无账户自动开户，落充值流水。
	Recharge(ctx context.Context, tenantID, holderID uint64, amount int64, remark string) (Account, error)
	// Account 查询会员账户；不存在返回 ErrAccountNotFound。
	Account(ctx context.Context, tenantID, holderID uint64) (Account, error)
	// Balance 查询会员余额；未开户返回 0。
	Balance(ctx context.Context, tenantID, holderID uint64) (int64, error)
	// Deduct 消费抵扣：实扣不超过余额，返回实扣金额。
	Deduct(ctx context.Context, tenantID, holderID uint64, amount int64, ref Ref) (int64, error)
	// Accounts 分页查询会员账户（不联查持有者资料）。
	Accounts(ctx context.Context, tenantID uint64, q PageQuery) (AccountPage, error)
	// Transactions 分页查询会员资金流水。
	Transactions(ctx context.Context, tenantID, holderID uint64, q PageQuery) (TransactionPage, error)
}

// NormalizeQuery 规范化分页参数：page<1 归一为 1，size<1 归一为 20、>100 归一为 100。
// 导出供契约实现方复用。
func NormalizeQuery(q PageQuery) PageQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 100 {
		q.PageSize = 100
	}
	return q
}
