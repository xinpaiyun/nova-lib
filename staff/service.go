package staff

import (
	"context"
	"errors"
	"strings"

	"github.com/xinpaiyun/nova-lib/identity"
	"gorm.io/gorm"
)

// UserResolver 把手机号解析成全局用户 ID。lib 只用它读 users，永不创建。
type UserResolver interface {
	// UserByPhone 查询已注册且启用的用户；未注册返回 ErrUserNotRegistered。
	UserByPhone(ctx context.Context, phone string) (uint64, error)
}

// GormUserResolver 基于 identity.users 的默认实现。
type GormUserResolver struct {
	db *gorm.DB
}

// NewGormUserResolver 创建默认用户解析器。
func NewGormUserResolver(db *gorm.DB) *GormUserResolver { return &GormUserResolver{db: db} }

// UserByPhone 查询手机号对应的已注册用户。
// users 表的 phone 只有普通索引，存量可能存在重复，固定取最早一条以保证幂等。
func (r *GormUserResolver) UserByPhone(ctx context.Context, phone string) (uint64, error) {
	var user identity.User
	err := r.db.WithContext(ctx).
		Where("phone = ? AND deleted_at IS NULL", strings.TrimSpace(phone)).
		Order("id ASC").Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrUserNotRegistered
	}
	if err != nil {
		return 0, err
	}
	if user.Status != identity.StatusEnabled {
		return 0, ErrUserDisabled
	}
	return user.ID, nil
}

// CreateMemberInput 是建员工入参。用户必须已注册，lib 不代建账号。
type CreateMemberInput struct {
	OrgID uint64
	// Phone 邀请手机号，用于定位全局用户；与 UserID 二选一，优先 UserID。
	Phone string
	// UserID 直接指定全局用户（迁移/内部调用），>0 时忽略 Phone。
	UserID     uint64
	Name       string
	EmployeeNo string
	Position   string
	RoleCode   string
}

// Service 编排员工域的业务规则：权限校验、用户已注册校验、开关读写。
// 数据访问全部经由 Source/Writer，因此已上线项目装配 AdapterSource 后
// 业务规则完全复用，不需要再写一遍。
type Service struct {
	src    Source
	writer Writer
	users  UserResolver
}

// NewService 使用标准表构造服务。
func NewService(db *gorm.DB) *Service {
	src := NewGormSource(db)
	return &Service{src: src, writer: src, users: NewGormUserResolver(db)}
}

// NewServiceWith 使用自定义数据源构造服务（AdapterSource 场景）。
func NewServiceWith(src Source, writer Writer, users UserResolver) *Service {
	return &Service{src: src, writer: writer, users: users}
}

// Source 暴露只读数据源，供查询复用。
func (s *Service) Source() Source { return s.src }

// CreateMember 创建员工账号。
//
// 规则：
//   - 操作者必须具备管理权限（manager 及以上）；
//   - 手机号必须已注册，未注册返回 ErrUserNotRegistered，不代建用户；
//   - 角色码必须是已注册的管理权限角色。
func (s *Service) CreateMember(ctx context.Context, actor *Identity, in CreateMemberInput) (*StaffMember, error) {
	if actor == nil || !actor.CanManage() {
		return nil, ErrPermissionDenied
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrMemberInputInvalid
	}
	role, ok := ResolveRole(in.RoleCode)
	if !ok {
		return nil, ErrRoleUnknown
	}

	userID := in.UserID
	if userID == 0 {
		phone := strings.TrimSpace(in.Phone)
		if err := ValidatePhone(phone); err != nil {
			return nil, err
		}
		resolved, err := s.users.UserByPhone(ctx, phone)
		if err != nil {
			return nil, err
		}
		userID = resolved
	}

	return s.writer.CreateMember(ctx, MemberInput{
		TenantID:   actor.TenantID,
		OrgID:      in.OrgID,
		UserID:     userID,
		Name:       name,
		EmployeeNo: in.EmployeeNo,
		Position:   in.Position,
		RoleCode:   role.Code,
	})
}

// UpdateMember 更新员工资料与角色。
func (s *Service) UpdateMember(ctx context.Context, actor *Identity, memberID uint64, in CreateMemberInput) error {
	if actor == nil || !actor.CanManage() {
		return ErrPermissionDenied
	}
	return s.writer.UpdateMember(ctx, actor.TenantID, memberID, MemberInput{
		OrgID:      in.OrgID,
		Name:       in.Name,
		EmployeeNo: in.EmployeeNo,
		Position:   in.Position,
		RoleCode:   in.RoleCode,
	})
}

// SetStatus 启用/禁用员工。禁用即时生效，下次请求即被 Resolve 拒绝。
func (s *Service) SetStatus(ctx context.Context, actor *Identity, memberID uint64, status int8) error {
	if actor == nil || !actor.CanManage() {
		return ErrPermissionDenied
	}
	// 防止管理员把自己锁在门外。
	if actor.MemberID == memberID && status == StatusDisabled {
		return ErrPermissionDenied
	}
	return s.writer.SetStatus(ctx, actor.TenantID, memberID, status)
}

// AddCapability 为员工开启业务开关（如「服务者」）。开关规则由 CapabilitySpec 决定。
func (s *Service) AddCapability(ctx context.Context, actor *Identity, memberID uint64, in CapabilityInput) (*StaffCapability, error) {
	if actor == nil || !actor.CanManage() {
		return nil, ErrPermissionDenied
	}
	in.TenantID = actor.TenantID
	in.MemberID = memberID
	return s.writer.AddCapability(ctx, actor.TenantID, memberID, in)
}

// RemoveCapability 关闭员工的业务开关。
func (s *Service) RemoveCapability(ctx context.Context, actor *Identity, memberID uint64, code string, subjectID uint64) error {
	if actor == nil || !actor.CanManage() {
		return ErrPermissionDenied
	}
	return s.writer.RemoveCapability(ctx, actor.TenantID, memberID, code, subjectID)
}

// ListMembers 管理端分页查询员工。
func (s *Service) ListMembers(ctx context.Context, actor *Identity, filter MemberFilter) ([]StaffMember, int64, error) {
	if actor == nil || !actor.CanManage() {
		return nil, 0, ErrPermissionDenied
	}
	return s.src.Members(ctx, actor.TenantID, filter)
}

// MemberIdentity 返回指定员工的身份快照（含开关与范围），供管理端查看权限。
func (s *Service) MemberIdentity(ctx context.Context, actor *Identity, memberID uint64) (*Identity, error) {
	if actor == nil || !actor.CanManage() {
		return nil, ErrPermissionDenied
	}
	return ResolveMember(ctx, s.src, actor.TenantID, memberID)
}

// ValidatePhone 校验中国大陆手机号：11 位数字且以 1 开头。
// 导出供会员域后台建档等场景复用同一口径。
func ValidatePhone(phone string) error {
	if len(phone) != 11 || phone[0] != '1' {
		return ErrPhoneInvalid
	}
	for i := 0; i < len(phone); i++ {
		if phone[i] < '0' || phone[i] > '9' {
			return ErrPhoneInvalid
		}
	}
	return nil
}
