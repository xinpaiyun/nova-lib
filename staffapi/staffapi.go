package staffapi

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/response"
	"github.com/xinpaiyun/nova-lib/staff"
)

// StaffAPI 提供员工域标准 handler。所有写操作要求 manager 及以上。
type StaffAPI struct {
	svc *staff.Service
}

// NewStaffAPI 创建标准 handler 集合。
func NewStaffAPI(svc *staff.Service) *StaffAPI { return &StaffAPI{svc: svc} }

// CreateMemberRequest 是 POST /v1/staff/members 的请求体。
type CreateMemberRequest struct {
	OrgID      uint64 `json:"orgId"`
	Phone      string `json:"phone"`
	UserID     uint64 `json:"userId"`
	Name       string `json:"name"`
	EmployeeNo string `json:"employeeNo"`
	Position   string `json:"position"`
	RoleCode   string `json:"roleCode"`
}

// UpdateMemberRequest 是 PATCH /v1/staff/members/:id 的请求体，字段均为可选。
type UpdateMemberRequest struct {
	OrgID      *uint64 `json:"orgId"`
	Name       *string `json:"name"`
	EmployeeNo *string `json:"employeeNo"`
	Position   *string `json:"position"`
	RoleCode   *string `json:"roleCode"`
	Status     *int8   `json:"status"`
}

// SetCapabilityRequest 是 POST /v1/staff/members/:id/caps 的请求体。
type SetCapabilityRequest struct {
	Code      string `json:"code"`
	SubjectID uint64 `json:"subjectId"`
	Enabled   *bool  `json:"enabled"`
}

// MemberListResponse 是员工列表的标准响应。
type MemberListResponse struct {
	List     []staff.StaffMember `json:"list"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

// CapabilityOption 是开关列表的标准响应项（不暴露内部 Guard 函数）。
type CapabilityOption struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	SubjectKind     string `json:"subjectKind"`
	SubjectRequired bool   `json:"subjectRequired"`
	Multiple        bool   `json:"multiple"`
	ScopeEffect     string `json:"scopeEffect"`
	WorkbenchAccess bool   `json:"workbenchAccess"`
}

// IdentityOption 是「我可进入的租户」标准响应项，用于商家态切换。
type IdentityOption struct {
	TenantID  uint64 `json:"tenantId"`
	OrgID     uint64 `json:"orgId"`
	MemberID  uint64 `json:"memberId"`
	Name      string `json:"name"`
	RoleCode  string `json:"roleCode"`
	RoleName  string `json:"roleName"`
	CanManage bool   `json:"canManage"`
}

// ListMembers 处理 GET /v1/staff/members。
func (h *StaffAPI) ListMembers(ctx context.Context, c *app.RequestContext) {
	actor, ok := mustIdentity(c)
	if !ok {
		return
	}
	page, pageSize, ok := PageQuery(c)
	if !ok {
		WriteError(c, BadRequestError("分页参数不正确"))
		return
	}
	orgID, ok := QueryUint64(c, "orgId")
	if !ok {
		WriteError(c, BadRequestError("参数 orgId 不正确"))
		return
	}
	status, ok := QueryStatus(c)
	if !ok {
		WriteError(c, BadRequestError("参数 status 不正确"))
		return
	}

	list, total, err := h.svc.ListMembers(ctx, actor, staff.MemberFilter{
		OrgID:    orgID,
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Status:   status,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		WriteError(c, err)
		return
	}
	response.Success(c, MemberListResponse{List: list, Total: total, Page: page, PageSize: pageSize})
}

// CreateMember 处理 POST /v1/staff/members。手机号未注册返回 1301。
func (h *StaffAPI) CreateMember(ctx context.Context, c *app.RequestContext) {
	actor, ok := mustIdentity(c)
	if !ok {
		return
	}
	var req CreateMemberRequest
	if !DecodeJSON(c, &req) {
		return
	}
	member, err := h.svc.CreateMember(ctx, actor, staff.CreateMemberInput{
		OrgID:      req.OrgID,
		Phone:      req.Phone,
		UserID:     req.UserID,
		Name:       req.Name,
		EmployeeNo: req.EmployeeNo,
		Position:   req.Position,
		RoleCode:   req.RoleCode,
	})
	if err != nil {
		WriteError(c, err)
		return
	}
	response.Success(c, member)
}

// UpdateMember 处理 PATCH /v1/staff/members/:id；未传字段保持原值。
func (h *StaffAPI) UpdateMember(ctx context.Context, c *app.RequestContext) {
	actor, ok := mustIdentity(c)
	if !ok {
		return
	}
	memberID, ok := ParamID(c, "id")
	if !ok {
		return
	}
	var req UpdateMemberRequest
	if !DecodeJSON(c, &req) {
		return
	}

	existing, err := h.svc.Source().MemberByID(ctx, actor.TenantID, memberID)
	if err != nil {
		WriteError(c, err)
		return
	}
	in := staff.CreateMemberInput{
		OrgID:      existing.OrgID,
		Name:       existing.Name,
		EmployeeNo: existing.EmployeeNo,
		Position:   existing.Position,
		RoleCode:   existing.RoleCode,
	}
	if req.OrgID != nil {
		in.OrgID = *req.OrgID
	}
	if req.Name != nil {
		in.Name = *req.Name
	}
	if req.EmployeeNo != nil {
		in.EmployeeNo = *req.EmployeeNo
	}
	if req.Position != nil {
		in.Position = *req.Position
	}
	if req.RoleCode != nil {
		in.RoleCode = *req.RoleCode
	}

	if err := h.svc.UpdateMember(ctx, actor, memberID, in); err != nil {
		WriteError(c, err)
		return
	}
	if req.Status != nil && *req.Status != existing.Status {
		if err := h.svc.SetStatus(ctx, actor, memberID, *req.Status); err != nil {
			WriteError(c, err)
			return
		}
	}

	updated, err := h.svc.Source().MemberByID(ctx, actor.TenantID, memberID)
	if err != nil {
		WriteError(c, err)
		return
	}
	response.Success(c, updated)
}

// SetCapability 处理 POST /v1/staff/members/:id/caps，按 enabled 开或关。
func (h *StaffAPI) SetCapability(ctx context.Context, c *app.RequestContext) {
	actor, ok := mustIdentity(c)
	if !ok {
		return
	}
	memberID, ok := ParamID(c, "id")
	if !ok {
		return
	}
	var req SetCapabilityRequest
	if !DecodeJSON(c, &req) {
		return
	}
	if req.Enabled == nil {
		WriteError(c, BadRequestError("请指定 enabled"))
		return
	}

	if *req.Enabled {
		capability, err := h.svc.AddCapability(ctx, actor, memberID, staff.CapabilityInput{
			Code:      req.Code,
			SubjectID: req.SubjectID,
		})
		if err != nil {
			WriteError(c, err)
			return
		}
		response.Success(c, capability)
		return
	}
	if err := h.svc.RemoveCapability(ctx, actor, memberID, req.Code, req.SubjectID); err != nil {
		WriteError(c, err)
		return
	}
	response.Success(c, nil)
}

// ListCapabilities 处理 GET /v1/staff/capabilities，供前端渲染开关列表。
func (h *StaffAPI) ListCapabilities(ctx context.Context, c *app.RequestContext) {
	if _, ok := mustIdentity(c); !ok {
		return
	}
	specs := staff.CapabilitySpecs()
	options := make([]CapabilityOption, 0, len(specs))
	for _, spec := range specs {
		options = append(options, CapabilityOption{
			Code:            spec.Code,
			Name:            spec.Name,
			SubjectKind:     spec.SubjectKind,
			SubjectRequired: spec.SubjectRequired,
			Multiple:        spec.Multiple,
			ScopeEffect:     string(spec.ScopeEffect),
			WorkbenchAccess: spec.WorkbenchAccess,
		})
	}
	response.Success(c, options)
}

// ListIdentities 处理 GET /v1/staff/identities，返回该用户可进入的租户。
func (h *StaffAPI) ListIdentities(ctx context.Context, c *app.RequestContext) {
	userID, ok := SessionUserID(ctx, c)
	if !ok {
		WriteError(c, staff.ErrNotMember)
		return
	}
	members, err := h.svc.Source().MembersByUser(ctx, userID)
	if err != nil {
		WriteError(c, err)
		return
	}
	options := make([]IdentityOption, 0, len(members))
	for _, member := range members {
		role, ok := staff.ResolveRole(member.RoleCode)
		if !ok {
			role = staff.RoleSpec{Code: member.RoleCode}
		}
		options = append(options, IdentityOption{
			TenantID:  member.TenantID,
			OrgID:     member.OrgID,
			MemberID:  member.ID,
			Name:      member.Name,
			RoleCode:  role.Code,
			RoleName:  role.Name,
			CanManage: role.CanManage(),
		})
	}
	response.Success(c, options)
}

func mustIdentity(c *app.RequestContext) (*staff.Identity, bool) {
	identity, ok := staff.FromContext(c)
	if !ok || identity == nil {
		WriteError(c, staff.ErrPermissionDenied)
		return nil, false
	}
	return identity, true
}
