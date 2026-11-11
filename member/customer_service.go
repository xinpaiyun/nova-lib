package member

import (
	"context"
	"strings"

	"github.com/xinpaiyun/nova-lib/staff"
	"gorm.io/gorm"
)

// CustomerService 编排 C 端会员建档、查询、合并的业务规则。
//
// 用户解析复用 staff.UserResolver（同一个 users 表与同一套错误），
// 因此「手机号未注册」在员工与会员两个入口表现一致。
type CustomerService struct {
	src    CustomerSource
	writer CustomerWriter
	users  staff.UserResolver
}

// NewCustomerService 使用标准表构造服务。
func NewCustomerService(db *gorm.DB) *CustomerService {
	src := NewGormSource(db)
	return &CustomerService{src: src, writer: src, users: staff.NewGormUserResolver(db)}
}

// NewCustomerServiceWith 使用自定义数据源与用户解析器构造服务。
func NewCustomerServiceWith(src CustomerSource, writer CustomerWriter, users staff.UserResolver) *CustomerService {
	return &CustomerService{src: src, writer: writer, users: users}
}

// Source 暴露只读数据源。
func (s *CustomerService) Source() CustomerSource { return s.src }

// Create 扫码入会等以登录会话为准的建档。
//
// 规则：
//   - userID 必须 > 0（来自会话，不接受前端传入的 0 或空值）；
//   - 同租户已有会员时幂等返回已有记录，不重复建档；
//   - Source 为空时默认 scan_qr。
func (s *CustomerService) Create(ctx context.Context, in CustomerInput) (*CustomerMember, error) {
	if in.UserID == 0 {
		return nil, ErrCustomerUserRequired
	}
	if in.TenantID == 0 {
		return nil, ErrCustomerInputInvalid
	}
	return s.writer.CreateCustomer(ctx, in)
}

// CreateByPhone 是后台建档：操作者必须具备管理权限，手机号必须已注册。
// 未注册返回 staff.ErrUserNotRegistered，提示对方先扫码注册。
func (s *CustomerService) CreateByPhone(ctx context.Context, actor *staff.Identity, in CustomerInput) (*CustomerMember, error) {
	if err := requireManage(actor); err != nil {
		return nil, err
	}
	phone := strings.TrimSpace(in.Phone)
	if err := staff.ValidatePhone(phone); err != nil {
		return nil, err
	}
	userID, err := s.users.UserByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}

	in.TenantID = actor.TenantID
	in.UserID = userID
	in.Phone = phone
	if in.Source == "" {
		in.Source = CustomerSourceStaffCreated
	}
	return s.Create(ctx, in)
}

// GetByUser 查询当前登录用户在指定租户的会员关系（C 端“我的会员卡”）。
func (s *CustomerService) GetByUser(ctx context.Context, tenantID, userID uint64) (*CustomerMember, error) {
	if userID == 0 {
		return nil, ErrCustomerUserRequired
	}
	return s.src.CustomerByUser(ctx, tenantID, userID)
}

// GetByID 管理端按会员 ID 查询档案。
func (s *CustomerService) GetByID(ctx context.Context, actor *staff.Identity, id uint64) (*CustomerMember, error) {
	if err := requireManage(actor); err != nil {
		return nil, err
	}
	return s.src.CustomerByID(ctx, actor.TenantID, id)
}

// List 管理端分页查询会员。
func (s *CustomerService) List(ctx context.Context, actor *staff.Identity, filter CustomerFilter) ([]CustomerMember, int64, error) {
	if err := requireManage(actor); err != nil {
		return nil, 0, err
	}
	return s.src.Customers(ctx, actor.TenantID, filter)
}

// Update 管理端更新会员资料；用户绑定关系不允许改动。
func (s *CustomerService) Update(ctx context.Context, actor *staff.Identity, id uint64, in CustomerInput) error {
	if err := requireManage(actor); err != nil {
		return err
	}
	return s.writer.UpdateCustomer(ctx, actor.TenantID, id, in)
}

// Merge 合并重复会员：关系、积分与储值账户一并并入 intoID。
// hook 用于项目在同一事务内改写自有业务表外键，可为 nil。
func (s *CustomerService) Merge(ctx context.Context, actor *staff.Identity, fromIDs []uint64, intoID uint64, hook MergeHook) (*MergeResult, error) {
	if err := requireManage(actor); err != nil {
		return nil, err
	}
	return s.writer.MergeCustomers(ctx, actor.TenantID, fromIDs, intoID, hook)
}

func requireManage(actor *staff.Identity) error {
	if actor == nil || !actor.CanManage() {
		return staff.ErrPermissionDenied
	}
	return nil
}
