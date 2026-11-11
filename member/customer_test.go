package member

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xinpaiyun/nova-lib/identity"
	"github.com/xinpaiyun/nova-lib/staff"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newCustomerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取底层连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(ModelTypes()...); err != nil {
		t.Fatalf("迁移储值表失败: %v", err)
	}
	if err := db.AutoMigrate(CustomerModelTypes()...); err != nil {
		t.Fatalf("迁移会员表失败: %v", err)
	}
	if err := db.AutoMigrate(&identity.User{}); err != nil {
		t.Fatalf("迁移 users 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func seedMemberUser(t *testing.T, db *gorm.DB, id uint64, phone string, status int8) {
	t.Helper()
	user := &identity.User{ID: id, Phone: phone, PasswordHash: "x"}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	// status 带 default:1 标签，GORM 插入时会忽略零值，因此单独更新。
	if status != identity.StatusEnabled {
		if err := db.Model(&identity.User{}).Where("id = ?", id).
			Update("status", status).Error; err != nil {
			t.Fatalf("更新用户状态失败: %v", err)
		}
	}
}

func customerActor(t *testing.T, roleCode string) *staff.Identity {
	t.Helper()
	role, ok := staff.ResolveRole(roleCode)
	if !ok {
		t.Fatalf("角色 %q 未注册", roleCode)
	}
	return &staff.Identity{TenantID: 1, MemberID: 9, Role: role}
}

// TestCreateCustomerRequiresUser 验证 user_id=0 一律拒绝。
func TestCreateCustomerRequiresUser(t *testing.T) {
	db := newCustomerDB(t)
	svc := NewCustomerService(db)
	ctx := context.Background()

	if _, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 0}); !errors.Is(err, ErrCustomerUserRequired) {
		t.Fatalf("user_id=0 应报 ErrCustomerUserRequired, got %v", err)
	}
	if _, err := NewGormSource(db).CreateCustomer(ctx, CustomerInput{TenantID: 1, UserID: 0}); !errors.Is(err, ErrCustomerUserRequired) {
		t.Fatalf("写入层同样应拒绝 user_id=0, got %v", err)
	}
	if _, err := svc.Create(ctx, CustomerInput{TenantID: 0, UserID: 5}); !errors.Is(err, ErrCustomerInputInvalid) {
		t.Fatalf("缺租户应报 ErrCustomerInputInvalid, got %v", err)
	}
}

// TestCreateCustomerIdempotent 验证同租户幂等、跨租户独立。
func TestCreateCustomerIdempotent(t *testing.T) {
	svc := NewCustomerService(newCustomerDB(t))
	ctx := context.Background()

	first, err := svc.Create(ctx, CustomerInput{TenantID: 1, OrgID: 2, UserID: 11, Name: "张三"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if first.Status != CustomerStatusEnabled || first.Source != CustomerSourceScanQR {
		t.Fatalf("默认值错误: %+v", first)
	}

	second, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 11, Name: "张三改"})
	if err != nil {
		t.Fatalf("重复建档应幂等: %v", err)
	}
	if second.ID != first.ID || second.Name != "张三" {
		t.Fatalf("幂等返回不应覆盖已有档案: %+v", second)
	}

	other, err := svc.Create(ctx, CustomerInput{TenantID: 2, UserID: 11, Name: "张三"})
	if err != nil {
		t.Fatalf("跨租户建档失败: %v", err)
	}
	if other.ID == first.ID {
		t.Fatal("不同租户应为不同会员行")
	}
}

// TestCustomerQueryAndList 验证查询、检索与资料更新。
func TestCustomerQueryAndList(t *testing.T) {
	db := newCustomerDB(t)
	svc := NewCustomerService(db)
	ctx := context.Background()
	manager := customerActor(t, staff.RoleManager)

	c1, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 21, Name: "张三", Phone: "13800000001"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if _, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 22, Name: "李四", LevelCode: "vip"}); err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if _, err := svc.Create(ctx, CustomerInput{TenantID: 2, UserID: 23, Name: "张三"}); err != nil {
		t.Fatalf("建档失败: %v", err)
	}

	if got, err := svc.GetByUser(ctx, 1, 21); err != nil || got.ID != c1.ID {
		t.Fatalf("按用户查询错误: %+v err=%v", got, err)
	}
	if _, err := svc.GetByUser(ctx, 1, 24); !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("未建档应报 ErrCustomerNotFound, got %v", err)
	}
	if _, err := svc.GetByUser(ctx, 1, 0); !errors.Is(err, ErrCustomerUserRequired) {
		t.Fatalf("user_id=0 查询应拒绝, got %v", err)
	}

	list, total, err := svc.List(ctx, manager, CustomerFilter{})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("租户列表错误: total=%d len=%d err=%v", total, len(list), err)
	}
	if _, total, err = svc.List(ctx, manager, CustomerFilter{Keyword: "李四"}); err != nil || total != 1 {
		t.Fatalf("关键字检索错误: total=%d err=%v", total, err)
	}
	if _, total, err = svc.List(ctx, manager, CustomerFilter{LevelCode: "vip"}); err != nil || total != 1 {
		t.Fatalf("等级过滤错误: total=%d err=%v", total, err)
	}
	if _, _, err := svc.List(ctx, customerActor(t, staff.RoleStaff), CustomerFilter{}); !errors.Is(err, staff.ErrPermissionDenied) {
		t.Fatalf("普通员工不应能查会员列表, got %v", err)
	}

	if got, err := svc.GetByID(ctx, manager, c1.ID); err != nil || got.UserID != 21 {
		t.Fatalf("按 ID 查询错误: %+v err=%v", got, err)
	}
	if _, err := svc.GetByID(ctx, customerActor(t, staff.RoleStaff), c1.ID); !errors.Is(err, staff.ErrPermissionDenied) {
		t.Fatalf("普通员工不应能查会员档案, got %v", err)
	}

	if err := svc.Update(ctx, manager, c1.ID, CustomerInput{
		Name: "张三丰", LevelCode: "gold", Tags: `["老客"]`, Remark: "重点客户",
	}); err != nil {
		t.Fatalf("更新会员失败: %v", err)
	}
	updated, err := svc.GetByID(ctx, manager, c1.ID)
	if err != nil {
		t.Fatalf("更新后查询失败: %v", err)
	}
	if updated.Name != "张三丰" || updated.LevelCode != "gold" ||
		updated.Tags != `["老客"]` || updated.Remark != "重点客户" || updated.UserID != 21 {
		t.Fatalf("更新结果错误: %+v", updated)
	}
	if err := svc.Update(ctx, customerActor(t, staff.RoleStaff), c1.ID, CustomerInput{Name: "x"}); !errors.Is(err, staff.ErrPermissionDenied) {
		t.Fatalf("普通员工不应能改会员, got %v", err)
	}
	if err := svc.Update(ctx, manager, 999, CustomerInput{Name: "x"}); !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("不存在会员应报 ErrCustomerNotFound, got %v", err)
	}
}

// TestCreateByPhone 验证后台建档必须已注册，且权限/手机号统一校验。
func TestCreateByPhone(t *testing.T) {
	db := newCustomerDB(t)
	svc := NewCustomerService(db)
	ctx := context.Background()
	manager := customerActor(t, staff.RoleManager)

	seedMemberUser(t, db, 31, "13900000001", identity.StatusEnabled)
	seedMemberUser(t, db, 32, "13900000002", identity.StatusDisabled)

	if _, err := svc.CreateByPhone(ctx, nil, CustomerInput{Phone: "13900000001"}); !errors.Is(err, staff.ErrPermissionDenied) {
		t.Fatalf("空身份应拒绝, got %v", err)
	}
	if _, err := svc.CreateByPhone(ctx, customerActor(t, staff.RoleStaff), CustomerInput{Phone: "13900000001"}); !errors.Is(err, staff.ErrPermissionDenied) {
		t.Fatalf("普通员工应拒绝, got %v", err)
	}
	if _, err := svc.CreateByPhone(ctx, manager, CustomerInput{Phone: "123"}); !errors.Is(err, staff.ErrPhoneInvalid) {
		t.Fatalf("非法手机号应报 ErrPhoneInvalid, got %v", err)
	}
	if _, err := svc.CreateByPhone(ctx, manager, CustomerInput{Phone: "13900000003"}); !errors.Is(err, staff.ErrUserNotRegistered) {
		t.Fatalf("未注册手机号应报 ErrUserNotRegistered, got %v", err)
	}
	if _, err := svc.CreateByPhone(ctx, manager, CustomerInput{Phone: "13900000002"}); !errors.Is(err, staff.ErrUserDisabled) {
		t.Fatalf("停用手机号应报 ErrUserDisabled, got %v", err)
	}

	created, err := svc.CreateByPhone(ctx, manager, CustomerInput{Phone: " 13900000001 ", Name: "王五", OrgID: 3})
	if err != nil {
		t.Fatalf("后台建档失败: %v", err)
	}
	if created.UserID != 31 || created.TenantID != 1 || created.Source != CustomerSourceStaffCreated ||
		created.Phone != "13900000001" || created.OrgID != 3 {
		t.Fatalf("后台建档结果错误: %+v", created)
	}

	// 同一人再次扫码入会，幂等返回后台建的档案。
	again, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 31})
	if err != nil || again.ID != created.ID {
		t.Fatalf("扫码入会应幂等: %+v err=%v", again, err)
	}
}

// TestMergeCustomers 验证会员关系、积分与储值账户合并，以及各类拒绝。
func TestMergeCustomers(t *testing.T) {
	db := newCustomerDB(t)
	svc := NewCustomerService(db)
	wallet := NewGormStore(db)
	ctx := context.Background()
	manager := customerActor(t, staff.RoleManager)

	c1, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 41, Name: "主档案"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	c2, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 42, Name: "重复档案"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	c3, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 43, Name: "第三档案"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if err := db.Model(&CustomerMember{}).Where("id = ?", c2.ID).Update("points", int64(10)).Error; err != nil {
		t.Fatalf("设置积分失败: %v", err)
	}

	if _, err := wallet.Recharge(ctx, 1, c1.ID, 500, Ref{Type: RefTypeRecharge}, ""); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, err := wallet.Recharge(ctx, 1, c2.ID, 300, Ref{Type: RefTypeRecharge}, ""); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	var hooked [][2]uint64
	hook := func(_ *gorm.DB, tenantID, fromID, intoID uint64) error {
		if tenantID != 1 {
			t.Fatalf("hook 租户错误: %d", tenantID)
		}
		hooked = append(hooked, [2]uint64{fromID, intoID})
		return nil
	}

	result, err := svc.Merge(ctx, manager, []uint64{c2.ID, c2.ID, 0}, c1.ID, hook)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if len(result.Merged) != 1 || result.Merged[0] != c2.ID {
		t.Fatalf("实际合并列表错误: %v", result.Merged)
	}
	if result.Into == nil || result.Into.ID != c1.ID || result.Into.LegacyID != c2.ID || result.Into.Points != 10 {
		t.Fatalf("目标档案错误: %+v", result.Into)
	}
	if result.Balance != 800 {
		t.Fatalf("合并后余额应为 800, got %d", result.Balance)
	}
	if len(hooked) != 1 || hooked[0] != [2]uint64{c2.ID, c1.ID} {
		t.Fatalf("hook 调用错误: %v", hooked)
	}

	var merged CustomerMember
	if err := db.Where("id = ?", c2.ID).Take(&merged).Error; err != nil {
		t.Fatalf("读取被合并行失败: %v", err)
	}
	if merged.Status != CustomerStatusMerged || merged.MergedIntoID != c1.ID {
		t.Fatalf("被合并行状态错误: %+v", merged)
	}
	if _, err := svc.GetByUser(ctx, 1, 42); !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("已并入档案不应参与普通查询, got %v", err)
	}
	if _, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 42}); !errors.Is(err, ErrCustomerMerged) {
		t.Fatalf("已并入账号再次建档应报 ErrCustomerMerged, got %v", err)
	}

	account, err := wallet.FindAccount(ctx, 1, c1.ID)
	if err != nil || account.Balance != 800 || account.TotalRecharged != 800 {
		t.Fatalf("目标账户错误: %+v err=%v", account, err)
	}
	if _, err := wallet.FindAccount(ctx, 1, c2.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("旧账户应已并入删除, got %v", err)
	}
	txns, totalTxns, err := wallet.ListTransactions(ctx, 1, c1.ID, PageQuery{})
	if err != nil || totalTxns != 2 || len(txns) != 2 {
		t.Fatalf("流水应改写到目标会员: total=%d len=%d err=%v", totalTxns, len(txns), err)
	}

	// 重复合并同一目标幂等，不再产生变更。
	again, err := svc.Merge(ctx, manager, []uint64{c2.ID}, c1.ID, nil)
	if err != nil {
		t.Fatalf("重复合并应幂等: %v", err)
	}
	if len(again.Merged) != 0 || again.Balance != 800 {
		t.Fatalf("重复合并结果错误: %+v", again)
	}

	// 已并入其他目标的行不能再次改并。
	if _, err := svc.Merge(ctx, manager, []uint64{c2.ID}, c3.ID, nil); !errors.Is(err, ErrCustomerMergeInvalid) {
		t.Fatalf("改并到其他目标应报 ErrCustomerMergeInvalid, got %v", err)
	}
	if _, err := svc.Merge(ctx, manager, []uint64{c1.ID}, c1.ID, nil); !errors.Is(err, ErrCustomerMergeInvalid) {
		t.Fatalf("合并到自己应报 ErrCustomerMergeInvalid, got %v", err)
	}
	if _, err := svc.Merge(ctx, manager, nil, c1.ID, nil); !errors.Is(err, ErrCustomerMergeInvalid) {
		t.Fatalf("空合并列表应报 ErrCustomerMergeInvalid, got %v", err)
	}
	if _, err := svc.Merge(ctx, manager, []uint64{c1.ID}, 9999, nil); !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("目标不存在应报 ErrCustomerNotFound, got %v", err)
	}
	if _, err := svc.Merge(ctx, customerActor(t, staff.RoleStaff), []uint64{c1.ID}, c3.ID, nil); !errors.Is(err, staff.ErrPermissionDenied) {
		t.Fatalf("普通员工不应能合并, got %v", err)
	}

	// 跨租户来源不可合并。
	c4, err := svc.Create(ctx, CustomerInput{TenantID: 2, UserID: 44, Name: "异租户"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if _, err := svc.Merge(ctx, manager, []uint64{c4.ID}, c3.ID, nil); !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("跨租户来源应报 ErrCustomerNotFound, got %v", err)
	}
}

// TestMergeHookRollback 验证 hook 出错时整体回滚。
func TestMergeHookRollback(t *testing.T) {
	db := newCustomerDB(t)
	svc := NewCustomerService(db)
	ctx := context.Background()
	manager := customerActor(t, staff.RoleManager)

	into, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 51})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	from, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 52})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}

	boom := errors.New("业务外键改写失败")
	if _, err := svc.Merge(ctx, manager, []uint64{from.ID}, into.ID, func(*gorm.DB, uint64, uint64, uint64) error {
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("hook 错误应透传, got %v", err)
	}

	still, err := svc.GetByUser(ctx, 1, 52)
	if err != nil || still.Status != CustomerStatusEnabled || still.MergedIntoID != 0 {
		t.Fatalf("回滚后来源档案不应变更: %+v err=%v", still, err)
	}
}

// TestMergeWithoutWallet 验证无储值账户的会员也可正常合并。
func TestMergeWithoutWallet(t *testing.T) {
	db := newCustomerDB(t)
	svc := NewCustomerService(db)
	ctx := context.Background()

	into, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 61})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	from, err := svc.Create(ctx, CustomerInput{TenantID: 1, UserID: 62})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}

	result, err := svc.Merge(ctx, customerActor(t, staff.RoleManager), []uint64{from.ID}, into.ID, nil)
	if err != nil {
		t.Fatalf("无账户合并失败: %v", err)
	}
	if result.Balance != 0 || result.Into.LegacyID != from.ID {
		t.Fatalf("无账户合并结果错误: %+v", result)
	}
}
