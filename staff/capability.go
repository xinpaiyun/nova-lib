package staff

import (
	"context"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// ScopeEffect 描述某个开关对数据范围的影响方式。
type ScopeEffect string

const (
	// ScopeEffectNone 不影响数据范围，仅作为身份标记（如「可带教」「可收银」）。
	ScopeEffectNone ScopeEffect = "none"
	// ScopeEffectSelfBySubject 按关联档案收敛到本人（如「服务者」→ therapist_id）。
	ScopeEffectSelfBySubject ScopeEffect = "self_by_subject"
)

// CapabilityInput 是开关相关的业务校验入参。
type CapabilityInput struct {
	TenantID  uint64
	MemberID  uint64
	Code      string
	SubjectID uint64
}

// CapabilitySpec 描述一个业务开关。开关是业务概念，由项目在 bootstrap 阶段注册，
// lib 不硬编码任何具体开关码。
type CapabilitySpec struct {
	// Code 开关唯一码，项目内唯一，如 practitioner / teacher / cashier。
	Code string
	// Name 展示名，如「服务者」「收银」。
	Name string
	// SubjectKind 关联档案类型（therapist / teacher_profile / 空表示无需档案）。
	SubjectKind string
	// SubjectRequired 开启时是否必须选择档案。
	SubjectRequired bool
	// Multiple 是否允许同一员工挂接多个档案。
	Multiple bool
	// ScopeEffect 是否影响数据范围。
	ScopeEffect ScopeEffect
	// WorkbenchAccess 仅持有该开关的员工是否可进入商家工作台。
	WorkbenchAccess bool
	// Guard 可选的业务校验，如校验档案属于同租户。
	Guard func(ctx context.Context, db *gorm.DB, in CapabilityInput) error
}

type capabilityRegistryStore struct {
	sync.RWMutex
	specs map[string]CapabilitySpec
}

var capabilityRegistry = &capabilityRegistryStore{specs: map[string]CapabilitySpec{}}

// RegisterCapability 注册业务开关，重复或非法定义返回错误。
func RegisterCapability(spec CapabilitySpec) error {
	code := strings.TrimSpace(spec.Code)
	if code == "" {
		return ErrCapabilityCodeEmpty
	}
	switch spec.ScopeEffect {
	case "", ScopeEffectNone:
		spec.ScopeEffect = ScopeEffectNone
	case ScopeEffectSelfBySubject:
	default:
		return ErrScopeEffectInvalid
	}
	if spec.SubjectRequired && strings.TrimSpace(spec.SubjectKind) == "" {
		return ErrCapabilitySubjectKindMissing
	}
	spec.Code = code
	spec.SubjectKind = strings.TrimSpace(spec.SubjectKind)
	capabilityRegistry.Lock()
	defer capabilityRegistry.Unlock()
	if _, ok := capabilityRegistry.specs[code]; ok {
		return ErrCapabilitySpecDuplicate
	}
	capabilityRegistry.specs[code] = spec
	return nil
}

// CapabilitySpecByCode 按开关码查找定义。
func CapabilitySpecByCode(code string) (CapabilitySpec, bool) {
	capabilityRegistry.RLock()
	defer capabilityRegistry.RUnlock()
	spec, ok := capabilityRegistry.specs[strings.TrimSpace(code)]
	return spec, ok
}

// CapabilitySpecs 返回已注册的全部开关，按 Code 升序，供前端渲染开关列表。
func CapabilitySpecs() []CapabilitySpec {
	capabilityRegistry.RLock()
	defer capabilityRegistry.RUnlock()
	result := make([]CapabilitySpec, 0, len(capabilityRegistry.specs))
	for _, spec := range capabilityRegistry.specs {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}

// AffectsScope 报告该开关是否影响数据范围（self_by_subject）。
func (s CapabilitySpec) AffectsScope() bool {
	return s.ScopeEffect == ScopeEffectSelfBySubject
}

// Validate 校验一个开关实例是否符合定义。只做不依赖数据库的纯校验；
// 跨员工的档案占用唯一性（ErrCapabilitySubjectTaken）与 Guard 业务校验
// 由写入层（Service）结合存储执行。
func (s CapabilitySpec) Validate(in CapabilityInput) error {
	if s.SubjectRequired {
		if in.SubjectID == 0 {
			return ErrCapabilitySubjectRequired
		}
		return nil
	}
	if in.SubjectID != 0 {
		return ErrCapabilitySubjectNotAllowed
	}
	return nil
}
