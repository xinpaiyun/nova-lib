// Package memberapi 提供 C 端会员的标准 HTTP handler 与固定错误码契约。
//
//	GET  /v1/customers
//	POST /v1/customers          # 扫码入会（user_id 取自会话，幂等）
//	POST /v1/customers/admin    # 后台建档（按手机号，要求已注册）
//	POST /v1/customers/:id/merge
//
// 管理类接口挂载前提：鉴权中间件 + staff.Required(src)；
// 扫码入会只要求登录态，不要求员工身份。
package memberapi

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/apperror"
	"github.com/xinpaiyun/nova-lib/member"
	"github.com/xinpaiyun/nova-lib/response"
	"github.com/xinpaiyun/nova-lib/staff"
	"github.com/xinpaiyun/nova-lib/staffapi"
)

// MemberAPI 提供会员域标准 handler。
type MemberAPI struct {
	svc *member.CustomerService
	// MergeHook 可选的业务外键改写钩子，由项目按需赋值。
	MergeHook member.MergeHook
}

// NewMemberAPI 创建标准 handler 集合。
func NewMemberAPI(svc *member.CustomerService) *MemberAPI { return &MemberAPI{svc: svc} }

// CreateCustomerRequest 是扫码入会与后台建档共用的请求体。
type CreateCustomerRequest struct {
	OrgID     uint64 `json:"orgId"`
	Name      string `json:"name"`
	Nickname  string `json:"nickname"`
	Phone     string `json:"phone"`
	Gender    string `json:"gender"`
	Birthday  string `json:"birthday"`
	LevelCode string `json:"levelCode"`
	Tags      string `json:"tags"`
	Remark    string `json:"remark"`
	Source    string `json:"source"`
}

// MergeCustomersRequest 是 POST /v1/customers/:id/merge 的请求体。
type MergeCustomersRequest struct {
	FromIDs []uint64 `json:"fromIds"`
}

// CustomerListResponse 是会员列表的标准响应。
type CustomerListResponse struct {
	List     []member.CustomerMember `json:"list"`
	Total    int64                   `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"pageSize"`
}

// MapError 把会员域错误映射成固定错误码，其余交给员工域映射。
func MapError(err error) apperror.Error {
	var appErr apperror.Error
	if errors.As(err, &appErr) {
		return appErr
	}
	switch {
	case errors.Is(err, member.ErrCustomerUserRequired),
		errors.Is(err, member.ErrCustomerInputInvalid),
		errors.Is(err, member.ErrCustomerMergeInvalid):
		return apperror.NewWithCode(400, apperror.CodeBadRequest, err.Error())
	case errors.Is(err, member.ErrCustomerNotFound):
		return apperror.NewWithCode(404, apperror.CodeNotFound, err.Error())
	case errors.Is(err, member.ErrCustomerMerged):
		return apperror.NewWithCode(409, apperror.CodeConflict, err.Error())
	default:
		return staffapi.MapError(err)
	}
}

// List 处理 GET /v1/customers（管理端）。
func (h *MemberAPI) List(ctx context.Context, c *app.RequestContext) {
	actor, ok := connectedStaff(c)
	if !ok {
		return
	}
	page, pageSize, ok := staffapi.PageQuery(c)
	if !ok {
		staffapi.WriteError(c, staffapi.BadRequestError("分页参数不正确"))
		return
	}
	orgID, ok := staffapi.QueryUint64(c, "orgId")
	if !ok {
		staffapi.WriteError(c, staffapi.BadRequestError("参数 orgId 不正确"))
		return
	}
	status, ok := staffapi.QueryStatus(c)
	if !ok {
		staffapi.WriteError(c, staffapi.BadRequestError("参数 status 不正确"))
		return
	}

	list, total, err := h.svc.List(ctx, actor, member.CustomerFilter{
		OrgID:     orgID,
		Keyword:   strings.TrimSpace(c.Query("keyword")),
		LevelCode: strings.TrimSpace(c.Query("levelCode")),
		Status:    status,
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		response.AppError(c, MapError(err))
		return
	}
	response.Success(c, CustomerListResponse{List: list, Total: total, Page: page, PageSize: pageSize})
}

// Create 处理 POST /v1/customers：user_id 与租户取自登录会话。
func (h *MemberAPI) Create(ctx context.Context, c *app.RequestContext) {
	userID, ok := staffapi.SessionUserID(ctx, c)
	if !ok {
		response.AppError(c, apperror.ErrUnauthorized)
		return
	}
	tenantID := staffapi.SessionTenantID(ctx, c)
	if tenantID == 0 {
		staffapi.WriteError(c, staffapi.BadRequestError("缺少租户"))
		return
	}
	var req CreateCustomerRequest
	if !staffapi.DecodeJSON(c, &req) {
		return
	}
	customer, err := h.svc.Create(ctx, member.CustomerInput{
		TenantID:  tenantID,
		OrgID:     req.OrgID,
		UserID:    userID,
		Name:      req.Name,
		Nickname:  req.Nickname,
		Phone:     req.Phone,
		Gender:    req.Gender,
		Birthday:  req.Birthday,
		LevelCode: req.LevelCode,
		Tags:      req.Tags,
		Remark:    req.Remark,
		Source:    req.Source,
	})
	if err != nil {
		response.AppError(c, MapError(err))
		return
	}
	response.Success(c, customer)
}

// CreateByPhone 处理 POST /v1/customers/admin：手机号必须已注册，否则 1301。
func (h *MemberAPI) CreateByPhone(ctx context.Context, c *app.RequestContext) {
	actor, ok := connectedStaff(c)
	if !ok {
		return
	}
	var req CreateCustomerRequest
	if !staffapi.DecodeJSON(c, &req) {
		return
	}
	customer, err := h.svc.CreateByPhone(ctx, actor, member.CustomerInput{
		OrgID:     req.OrgID,
		Name:      req.Name,
		Nickname:  req.Nickname,
		Phone:     req.Phone,
		Gender:    req.Gender,
		Birthday:  req.Birthday,
		LevelCode: req.LevelCode,
		Tags:      req.Tags,
		Remark:    req.Remark,
		Source:    req.Source,
	})
	if err != nil {
		response.AppError(c, MapError(err))
		return
	}
	response.Success(c, customer)
}

// Merge 处理 POST /v1/customers/:id/merge，路径 ID 为合并目标。
func (h *MemberAPI) Merge(ctx context.Context, c *app.RequestContext) {
	actor, ok := connectedStaff(c)
	if !ok {
		return
	}
	intoID, ok := staffapi.ParamID(c, "id")
	if !ok {
		return
	}
	var req MergeCustomersRequest
	if !staffapi.DecodeJSON(c, &req) {
		return
	}
	result, err := h.svc.Merge(ctx, actor, req.FromIDs, intoID, h.MergeHook)
	if err != nil {
		response.AppError(c, MapError(err))
		return
	}
	response.Success(c, result)
}

func connectedStaff(c *app.RequestContext) (*staff.Identity, bool) {
	identity, ok := staff.FromContext(c)
	if !ok || identity == nil {
		staffapi.WriteError(c, staff.ErrPermissionDenied)
		return nil, false
	}
	return identity, true
}
