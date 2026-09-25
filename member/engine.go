package member

import "context"

// maxRechargeAmount 单笔充值上限：100 万元（分）。
const maxRechargeAmount = 100_000_000

// engine Wallet 契约的参考实现：业务规则（金额上限等）在引擎侧校验，数据访问委托给 Store。
type engine struct {
	store Store
}

// 编译期断言：参考引擎满足 Wallet 契约。
var _ Wallet = (*engine)(nil)

// NewEngine 创建会员储值参考引擎。
// store 可为 NewGormStore 返回的参考实现，也可为项目自建的数据访问实现。
func NewEngine(store Store) Wallet {
	return &engine{store: store}
}

// Recharge 会员充值：无账户自动开户，落充值流水（RefType=recharge）。
func (e *engine) Recharge(ctx context.Context, tenantID, holderID uint64, amount int64, remark string) (Account, error) {
	if amount <= 0 || amount > maxRechargeAmount {
		return Account{}, ErrInvalidAmount
	}
	account, err := e.store.Recharge(ctx, tenantID, holderID, amount, Ref{Type: RefTypeRecharge}, remark)
	if err != nil {
		return Account{}, err
	}
	return accountOf(account), nil
}

// Account 查询会员账户；不存在返回 ErrAccountNotFound。
func (e *engine) Account(ctx context.Context, tenantID, holderID uint64) (Account, error) {
	account, err := e.store.FindAccount(ctx, tenantID, holderID)
	if err != nil {
		return Account{}, err
	}
	return accountOf(account), nil
}

// Balance 查询会员余额；未开户返回 0。
func (e *engine) Balance(ctx context.Context, tenantID, holderID uint64) (int64, error) {
	return e.store.AccountBalance(ctx, tenantID, holderID)
}

// Deduct 消费抵扣：实扣不超过现有余额并落消费流水，返回实扣金额；
// 金额不大于 0 时不做任何操作返回 0。
func (e *engine) Deduct(ctx context.Context, tenantID, holderID uint64, amount int64, ref Ref) (int64, error) {
	if amount <= 0 {
		return 0, nil
	}
	return e.store.Deduct(ctx, tenantID, holderID, amount, ref)
}

// Accounts 分页查询会员账户（不联查持有者资料）。
func (e *engine) Accounts(ctx context.Context, tenantID uint64, q PageQuery) (AccountPage, error) {
	q = NormalizeQuery(q)
	rows, total, err := e.store.ListAccounts(ctx, tenantID, q)
	if err != nil {
		return AccountPage{}, err
	}
	items := make([]Account, 0, len(rows))
	for _, row := range rows {
		items = append(items, accountOf(row))
	}
	return AccountPage{Items: items, Total: total, Page: q.Page, Size: q.PageSize}, nil
}

// Transactions 分页查询会员资金流水。
func (e *engine) Transactions(ctx context.Context, tenantID, holderID uint64, q PageQuery) (TransactionPage, error) {
	q = NormalizeQuery(q)
	rows, total, err := e.store.ListTransactions(ctx, tenantID, holderID, q)
	if err != nil {
		return TransactionPage{}, err
	}
	items := make([]Transaction, 0, len(rows))
	for _, row := range rows {
		items = append(items, transactionOf(row))
	}
	return TransactionPage{Items: items, Total: total, Page: q.Page, Size: q.PageSize}, nil
}

// accountOf 将账户模型转换为契约视图。
func accountOf(m MemberAccount) Account {
	return Account{
		TenantID:       m.TenantID,
		HolderID:       m.HolderID,
		Balance:        m.Balance,
		TotalRecharged: m.TotalRecharged,
		TotalSpent:     m.TotalSpent,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// transactionOf 将流水模型转换为契约视图。
func transactionOf(m MemberTransaction) Transaction {
	return Transaction{
		ID:           m.ID,
		Type:         m.Type,
		Amount:       m.Amount,
		BalanceAfter: m.BalanceAfter,
		Ref:          Ref{Type: m.RefType, ID: m.RefID, No: m.RefNo},
		Remark:       m.Remark,
		CreatedAt:    m.CreatedAt,
	}
}
