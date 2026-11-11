package staff

import (
	"context"

	"gorm.io/gorm"
)

// ScopeColumn 描述业务表上用于范围过滤的列名。空列名表示该表不按此维度划分。
type ScopeColumn struct {
	// Org 门店/组织列名（如 store_id），org 范围按它收敛。
	Org string
	// Self 档案列名（如 therapist_id），self 范围按 SelfSubjectIDs 收敛。
	Self string
}

// ApplyScope 把身份的数据范围翻译成查询过滤条件，返回追加了条件的查询。
//
//   - ScopeAll  → 不追加条件（租户过滤由调用方保证，本函数不代劳）；
//   - ScopeOrg  → Org 列 = 身份的 OrgID；Org 列为空表示该表不按门店划分，放行；
//   - ScopeSelf → Self 列 IN (SelfSubjectIDs)；无档案集合生成恒假条件；
//   - ScopeNone → 恒假条件。
//
// 「无权限」一律翻译成恒假条件（1=0）而不是直接放行，保证漏配列名时
// 宁可查不到数据也不会泄露。
func (i *Identity) ApplyScope(db *gorm.DB, col ScopeColumn) *gorm.DB {
	switch i.Scope {
	case ScopeAll:
		return db
	case ScopeOrg:
		if col.Org == "" {
			return db
		}
		return db.Where(col.Org+" = ?", i.OrgID)
	case ScopeSelf:
		if col.Self == "" {
			// 调用方漏传档案列：恒假而不是拼出非法 SQL。
			return db.Where("1 = 0")
		}
		ids := i.SelfSubjectIDs()
		if len(ids) == 0 {
			return db.Where("1 = 0")
		}
		return db.Where(col.Self+" IN ?", ids)
	default:
		return db.Where("1 = 0")
	}
}

// ApplyScopeIn 集合型收敛：col IN (ids)，空集生成恒假条件。
//
// 适用于无法用单一列表达的口径（如「本人服务过的客户」= 预约 ∪ 理疗记录
// 命中的客户去重集合）。与 ApplyScope 不同，本函数不做范围放行——
// 管理身份可见多少完全由调用方计算的集合决定（如授权客户集合），
// lib 不擅自扩大可见范围。
func ApplyScopeIn(db *gorm.DB, col string, ids []uint64) *gorm.DB {
	if len(ids) == 0 {
		return db.Where("1 = 0")
	}
	return db.Where(col+" IN ?", ids)
}

// SelfSetResolver 由项目实现，为集合型「本人」范围提供可见对象 ID 集合。
// 例如「本人服务过的客户」：按身份查 bookings ∪ treatment_records 去重；
// 管理身份由项目返回全量授权集合。查询结果交给 ApplyScopeIn 落到 SQL。
type SelfSetResolver interface {
	SelfSet(ctx context.Context, id *Identity) ([]uint64, error)
}
