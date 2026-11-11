package staff

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// MemberFilter 员工列表查询条件。
type MemberFilter struct {
	OrgID    uint64
	Keyword  string // 姓名/工号模糊匹配
	Status   *int8  // nil 表示不限
	Page     int
	PageSize int
}

// Source 是员工域的只读数据源。GormSource 是标准实现；
// 已上线项目可用 AdapterSource 把存量表适配成同一套接口。
type Source interface {
	// MemberByUser 按 (tenant_id, user_id) 查员工；不存在返回 ErrMemberNotFound。
	MemberByUser(ctx context.Context, tenantID, userID uint64) (*StaffMember, error)
	// MembersByUser 返回该用户在所有租户的启用员工关系，用于商家态切换。
	MembersByUser(ctx context.Context, userID uint64) ([]StaffMember, error)
	// MemberByID 按主键查员工；不存在返回 ErrMemberNotFound。
	MemberByID(ctx context.Context, tenantID, id uint64) (*StaffMember, error)
	// Members 分页查询员工。
	Members(ctx context.Context, tenantID uint64, filter MemberFilter) ([]StaffMember, int64, error)
	// Capabilities 返回员工当前启用的开关实例。
	Capabilities(ctx context.Context, tenantID, memberID uint64) ([]StaffCapability, error)
}

// MemberInput 员工创建/更新入参。
type MemberInput struct {
	TenantID   uint64
	OrgID      uint64
	UserID     uint64
	Name       string
	EmployeeNo string
	Position   string
	RoleCode   string
}

// Writer 是员工域的写入接口。
type Writer interface {
	CreateMember(ctx context.Context, in MemberInput) (*StaffMember, error)
	UpdateMember(ctx context.Context, tenantID, id uint64, in MemberInput) error
	SetStatus(ctx context.Context, tenantID, id uint64, status int8) error
	AddCapability(ctx context.Context, tenantID, memberID uint64, in CapabilityInput) (*StaffCapability, error)
	RemoveCapability(ctx context.Context, tenantID, memberID uint64, code string, subjectID uint64) error
}

// GormSource 基于标准表 staff_members / staff_capabilities 实现 Source 与 Writer。
type GormSource struct {
	db *gorm.DB
}

// NewGormSource 创建标准表数据源。
func NewGormSource(db *gorm.DB) *GormSource { return &GormSource{db: db} }

// DB 暴露底层连接，供可选 Guard 校验使用。
func (s *GormSource) DB() *gorm.DB { return s.db }

// MemberByUser 按 (tenant_id, user_id) 查询未删除员工。
func (s *GormSource) MemberByUser(ctx context.Context, tenantID, userID uint64) (*StaffMember, error) {
	return s.memberBy(ctx, "tenant_id = ? AND user_id = ?", tenantID, userID)
}

// MembersByUser 返回该用户所有租户下的启用员工关系，按租户升序。
func (s *GormSource) MembersByUser(ctx context.Context, userID uint64) ([]StaffMember, error) {
	var members []StaffMember
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND deleted_at IS NULL", userID, StatusEnabled).
		Order("tenant_id ASC, id ASC").
		Find(&members).Error
	if err != nil {
		return nil, err
	}
	return members, nil
}

// MemberByID 按主键查询未删除员工。
func (s *GormSource) MemberByID(ctx context.Context, tenantID, id uint64) (*StaffMember, error) {
	return s.memberBy(ctx, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *GormSource) memberBy(ctx context.Context, where string, args ...any) (*StaffMember, error) {
	var member StaffMember
	err := s.db.WithContext(ctx).
		Where(where+" AND deleted_at IS NULL", args...).
		Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// Members 分页查询员工，按 ID 倒序。
func (s *GormSource) Members(ctx context.Context, tenantID uint64, filter MemberFilter) ([]StaffMember, int64, error) {
	// 用工厂函数构造查询，避免 Count 改写 Statement 后影响后续 Find。
	build := func() *gorm.DB {
		query := s.db.WithContext(ctx).Model(&StaffMember{}).
			Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
		if filter.OrgID > 0 {
			query = query.Where("org_id = ?", filter.OrgID)
		}
		if filter.Status != nil {
			query = query.Where("status = ?", *filter.Status)
		}
		if kw := strings.TrimSpace(filter.Keyword); kw != "" {
			like := "%" + kw + "%"
			query = query.Where("name LIKE ? OR employee_no LIKE ?", like, like)
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

	var list []StaffMember
	err := build().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Capabilities 返回员工当前启用的开关实例。
func (s *GormSource) Capabilities(ctx context.Context, tenantID, memberID uint64) ([]StaffCapability, error) {
	var list []StaffCapability
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND member_id = ? AND status = ?", tenantID, memberID, StatusEnabled).
		Order("code ASC, subject_id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// CreateMember 创建员工。同一租户同一用户只允许一条，冲突返回 ErrMemberExists。
func (s *GormSource) CreateMember(ctx context.Context, in MemberInput) (*StaffMember, error) {
	if in.TenantID == 0 || in.UserID == 0 || strings.TrimSpace(in.Name) == "" {
		return nil, ErrMemberInputInvalid
	}
	role, ok := ResolveRole(in.RoleCode)
	if !ok {
		return nil, ErrRoleUnknown
	}

	// 先查重，避免依赖数据库错误文本判断唯一键冲突。
	var count int64
	if err := s.db.WithContext(ctx).Model(&StaffMember{}).
		Where("tenant_id = ? AND user_id = ? AND deleted_at IS NULL", in.TenantID, in.UserID).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrMemberExists
	}

	member := &StaffMember{
		TenantID:   in.TenantID,
		OrgID:      in.OrgID,
		UserID:     in.UserID,
		Name:       strings.TrimSpace(in.Name),
		EmployeeNo: strings.TrimSpace(in.EmployeeNo),
		Position:   strings.TrimSpace(in.Position),
		RoleCode:   role.Code,
		Status:     StatusEnabled,
	}
	if err := s.db.WithContext(ctx).Create(member).Error; err != nil {
		if isDuplicateError(err) {
			return nil, ErrMemberExists
		}
		return nil, err
	}
	return member, nil
}

// UpdateMember 更新员工资料与角色。用户（登录身份）不允许改绑。
func (s *GormSource) UpdateMember(ctx context.Context, tenantID, id uint64, in MemberInput) error {
	updates := map[string]any{}
	if name := strings.TrimSpace(in.Name); name != "" {
		updates["name"] = name
	}
	if in.OrgID > 0 {
		updates["org_id"] = in.OrgID
	}
	updates["employee_no"] = strings.TrimSpace(in.EmployeeNo)
	updates["position"] = strings.TrimSpace(in.Position)
	if in.RoleCode != "" {
		role, ok := ResolveRole(in.RoleCode)
		if !ok {
			return ErrRoleUnknown
		}
		updates["role_code"] = role.Code
	}
	return s.updateMember(ctx, tenantID, id, updates)
}

// SetStatus 启用/禁用员工。
func (s *GormSource) SetStatus(ctx context.Context, tenantID, id uint64, status int8) error {
	if status != StatusEnabled && status != StatusDisabled {
		return ErrMemberInputInvalid
	}
	return s.updateMember(ctx, tenantID, id, map[string]any{"status": status})
}

func (s *GormSource) updateMember(ctx context.Context, tenantID, id uint64, updates map[string]any) error {
	// 先确认存在，避免 MySQL「更新相同值 affected=0」被误判为不存在。
	if _, err := s.MemberByID(ctx, tenantID, id); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&StaffMember{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).
		Updates(updates).Error
}

// AddCapability 开启或改绑一个开关实例。
//
// 规则：
//   - 开关必须已注册；
//   - 实例必须通过 CapabilitySpec.Validate（必选档案/禁止档案）；
//   - 需档案时，同一档案不能被其他员工占用（ErrCapabilitySubjectTaken）；
//   - 单实例开关（Multiple=false）改绑时把同码旧实例置为关闭，保留审计；
//   - 同 (member, code, subject) 重复开启是幂等的，直接置为启用。
func (s *GormSource) AddCapability(ctx context.Context, tenantID, memberID uint64, in CapabilityInput) (*StaffCapability, error) {
	spec, ok := CapabilitySpecByCode(in.Code)
	if !ok {
		return nil, ErrCapabilitySpecUnknown
	}
	in.TenantID, in.MemberID, in.Code = tenantID, memberID, spec.Code
	if err := spec.Validate(in); err != nil {
		return nil, err
	}

	if _, err := s.MemberByID(ctx, tenantID, memberID); err != nil {
		return nil, err
	}

	if in.SubjectID > 0 {
		var occupied int64
		err := s.db.WithContext(ctx).Model(&StaffCapability{}).
			Where("tenant_id = ? AND code = ? AND subject_id = ? AND member_id <> ? AND status = ?",
				tenantID, spec.Code, in.SubjectID, memberID, StatusEnabled).
			Count(&occupied).Error
		if err != nil {
			return nil, err
		}
		if occupied > 0 {
			return nil, ErrCapabilitySubjectTaken
		}
	}

	if spec.Guard != nil {
		if err := spec.Guard(ctx, s.db, in); err != nil {
			return nil, err
		}
	}

	if !spec.Multiple {
		if err := s.db.WithContext(ctx).Model(&StaffCapability{}).
			Where("tenant_id = ? AND member_id = ? AND code = ? AND subject_id <> ? AND status = ?",
				tenantID, memberID, spec.Code, in.SubjectID, StatusEnabled).
			Update("status", StatusDisabled).Error; err != nil {
			return nil, err
		}
	}

	var existing StaffCapability
	err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND member_id = ? AND code = ? AND subject_id = ?",
			tenantID, memberID, spec.Code, in.SubjectID).
		Take(&existing).Error
	if err == nil {
		if existing.Status != StatusEnabled {
			if err := s.db.WithContext(ctx).Model(&existing).
				Update("status", StatusEnabled).Error; err != nil {
				return nil, err
			}
			existing.Status = StatusEnabled
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	capability := &StaffCapability{
		TenantID:  tenantID,
		MemberID:  memberID,
		Code:      spec.Code,
		SubjectID: in.SubjectID,
		Status:    StatusEnabled,
	}
	if err := s.db.WithContext(ctx).Create(capability).Error; err != nil {
		return nil, err
	}
	return capability, nil
}

// RemoveCapability 关闭开关实例。subjectID=0 表示关闭该开关的全部实例，
// 用于前端「关掉整个开关」的单实例场景。
func (s *GormSource) RemoveCapability(ctx context.Context, tenantID, memberID uint64, code string, subjectID uint64) error {
	code = strings.TrimSpace(code)
	// 两次独立构造查询：Count 会改写 Statement，复用同一条链会让 Update 带上错误。
	var count int64
	countQuery := s.db.WithContext(ctx).Model(&StaffCapability{}).
		Where("tenant_id = ? AND member_id = ? AND code = ?", tenantID, memberID, code)
	if subjectID > 0 {
		countQuery = countQuery.Where("subject_id = ?", subjectID)
	}
	if err := countQuery.Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrCapabilitySpecUnknown
	}

	updateQuery := s.db.WithContext(ctx).Model(&StaffCapability{}).
		Where("tenant_id = ? AND member_id = ? AND code = ?", tenantID, memberID, code)
	if subjectID > 0 {
		updateQuery = updateQuery.Where("subject_id = ?", subjectID)
	}
	return updateQuery.Update("status", StatusDisabled).Error
}

// isDuplicateError 兼容 SQLite/MySQL 的唯一键冲突判断。
func isDuplicateError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicated key")
}
