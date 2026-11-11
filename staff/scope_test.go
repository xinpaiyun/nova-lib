package staff

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// scopeRow 范围过滤的行为验证表。
type scopeRow struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	TenantID    uint64
	StoreID     uint64
	TherapistID uint64
}

// TableName 指定测试表名。
func (scopeRow) TableName() string { return "scope_rows" }

func seedScopeRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	// 租户 1：门店 1 有技师 7/8/9，门店 2 有技师 7；租户 2 一条干扰数据。
	rows := []scopeRow{
		{TenantID: 1, StoreID: 1, TherapistID: 7},
		{TenantID: 1, StoreID: 1, TherapistID: 8},
		{TenantID: 1, StoreID: 1, TherapistID: 9},
		{TenantID: 1, StoreID: 2, TherapistID: 7},
		{TenantID: 2, StoreID: 3, TherapistID: 7},
	}
	if err := db.AutoMigrate(&scopeRow{}); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("写入测试数据失败: %v", err)
		}
	}
}

// countWith 统计追加范围条件后的行数（租户过滤由调用方负责，模拟真实用法）。
func countWith(t *testing.T, db *gorm.DB, apply func(*gorm.DB) *gorm.DB) int64 {
	t.Helper()
	var count int64
	query := apply(db.Model(&scopeRow{}).Where("tenant_id = ?", 1))
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	return count
}

var testCtx = context.Background()

// TestApplyScope 验证四档范围翻译成 SQL 过滤的行为。
func TestApplyScope(t *testing.T) {
	db := newStaffDB(t)
	seedScopeRows(t, db)

	col := ScopeColumn{Org: "store_id", Self: "therapist_id"}
	emptyCol := ScopeColumn{}

	cases := []struct {
		name string
		id   *Identity
		col  ScopeColumn
		want int64
	}{
		{"owner 全店 4 行", &Identity{
			Role: mustRole(t, RoleOwner), Scope: ScopeAll, OrgID: 1,
		}, col, 4},
		{"manager 本店 3 行", &Identity{
			Role: mustRole(t, RoleManager), Scope: ScopeOrg, OrgID: 1,
		}, col, 3},
		{"manager 表不分门店时放行", &Identity{
			Role: mustRole(t, RoleManager), Scope: ScopeOrg, OrgID: 1,
		}, emptyCol, 4},
		{"员工本人档案 7/8 共 3 行", &Identity{
			Role:         mustRole(t, RoleStaff),
			Scope:        ScopeSelf,
			SelfSubjects: map[string][]uint64{"practitioner": {7, 8}},
			Capabilities: []Capability{{Code: "practitioner", SubjectID: 7}, {Code: "practitioner", SubjectID: 8}},
		}, col, 3},
		{"员工无档案恒假", &Identity{
			Role: mustRole(t, RoleStaff), Scope: ScopeSelf,
		}, col, 0},
		{"Self 列缺失恒假", &Identity{
			Role:         mustRole(t, RoleStaff),
			Scope:        ScopeSelf,
			SelfSubjects: map[string][]uint64{"practitioner": {7}},
		}, ScopeColumn{Org: "store_id"}, 0},
		{"none 恒假", &Identity{
			Role: mustRole(t, RoleStaff), Scope: ScopeNone,
		}, col, 0},
	}
	for _, c := range cases {
		if got := countWith(t, db, func(q *gorm.DB) *gorm.DB { return c.id.ApplyScope(q, c.col) }); got != c.want {
			t.Errorf("%s: 行数 = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestApplyScopeIn 验证集合型收敛：空集恒假，非空按 IN 过滤。
func TestApplyScopeIn(t *testing.T) {
	db := newStaffDB(t)
	seedScopeRows(t, db)

	// 档案 7 命中租户 1 的 2 行。
	if got := countWith(t, db, func(q *gorm.DB) *gorm.DB {
		return ApplyScopeIn(q, "therapist_id", []uint64{7})
	}); got != 2 {
		t.Fatalf("集合 {7} 应命中 2 行, got %d", got)
	}
	// 空集恒假。
	if got := countWith(t, db, func(q *gorm.DB) *gorm.DB {
		return ApplyScopeIn(q, "therapist_id", nil)
	}); got != 0 {
		t.Fatalf("空集合应恒假, got %d", got)
	}
}

// TestSelfSetResolver 验证项目侧集合解析器与 ApplyScopeIn 的配合。
func TestSelfSetResolver(t *testing.T) {
	db := newStaffDB(t)
	seedScopeRows(t, db)

	resolver := servedCustomersResolver{bySubject: map[uint64][]uint64{
		7: {7},
		8: {8},
	}}

	// 技师 7 的员工：解析出集合 {7} → 2 行。
	id := &Identity{
		Role:         mustRole(t, RoleStaff),
		Scope:        ScopeSelf,
		SelfSubjects: map[string][]uint64{"practitioner": {7}},
	}
	ids, err := resolver.SelfSet(testCtx, id)
	if err != nil {
		t.Fatalf("解析集合失败: %v", err)
	}
	if got := countWith(t, db, func(q *gorm.DB) *gorm.DB {
		return ApplyScopeIn(q, "therapist_id", ids)
	}); got != 2 {
		t.Fatalf("集合型收敛应命中 2 行, got %d", got)
	}

	// 管理身份由项目返回全量集合 → 4 行。
	manager := &Identity{Role: mustRole(t, RoleOwner), Scope: ScopeAll}
	all, err := resolver.SelfSet(testCtx, manager)
	if err != nil {
		t.Fatalf("解析管理集合失败: %v", err)
	}
	if got := countWith(t, db, func(q *gorm.DB) *gorm.DB {
		return ApplyScopeIn(q, "therapist_id", all)
	}); got != 4 {
		t.Fatalf("全量集合应命中 4 行, got %d", got)
	}
}

// servedCustomersResolver 模拟项目侧实现：「本人服务过的对象」集合。
type servedCustomersResolver struct {
	bySubject map[uint64][]uint64
}

// SelfSet 按身份的档案集合求并集；管理身份返回全量。
func (r servedCustomersResolver) SelfSet(_ context.Context, id *Identity) ([]uint64, error) {
	if id.Scope.Allows(ScopeOrg) {
		return []uint64{7, 8, 9}, nil
	}
	seen := map[uint64]struct{}{}
	var result []uint64
	for _, subjects := range id.SelfSubjects {
		for _, subject := range subjects {
			for _, v := range r.bySubject[subject] {
				if _, ok := seen[v]; !ok {
					seen[v] = struct{}{}
					result = append(result, v)
				}
			}
		}
	}
	return result, nil
}

// TestIdentityContext 验证身份快照的上下文存取。
func TestIdentityContext(t *testing.T) {
	id := &Identity{TenantID: 1, UserID: 2, Name: "张三"}
	ctx := WithIdentity(testCtx, id)

	got, ok := IdentityFromContext(ctx)
	if !ok || got != id {
		t.Fatalf("上下文读取错误: %+v ok=%v", got, ok)
	}
	if _, err := RequireIdentity(ctx); err != nil {
		t.Fatalf("RequireIdentity 不应报错: %v", err)
	}

	if _, ok := IdentityFromContext(testCtx); ok {
		t.Fatal("空上下文不应读到身份")
	}
	if _, err := RequireIdentity(testCtx); !errors.Is(err, ErrNotMember) {
		t.Fatalf("缺失身份应报 ErrNotMember, got %v", err)
	}
}
