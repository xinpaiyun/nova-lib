package plan

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// defaultPayDeadline 订单默认支付有效期。
const defaultPayDeadline = 2 * time.Hour

// engine Engine 契约的参考实现：直接持有 GORM 连接，
// 购买生效/续费顺延/用量扣减等需要事务保证的流程在引擎内完成。
type engine struct {
	db *gorm.DB
}

// 编译期断言：参考引擎满足 Engine 契约。
var _ Engine = (*engine)(nil)

// NewEngine 创建套餐参考引擎。
func NewEngine(db *gorm.DB) Engine { return &engine{db: db} }

// ── 套餐目录 ──

// Plans 返回套餐目录（含权益与能力编码）。
func (e *engine) Plans(ctx context.Context, includeDisabled bool) ([]Plan, error) {
	query := e.db.WithContext(ctx).Model(&PlanModel{})
	if !includeDisabled {
		query = query.Where("status = ?", 1)
	}
	var rows []PlanModel
	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	ents, err := e.entitlementMap(ctx, planIDsOf(rows))
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(rows))
	for _, row := range rows {
		plans = append(plans, planOf(row, ents[row.ID]))
	}
	return plans, nil
}

// Plan 按 ID 查询套餐。
func (e *engine) Plan(ctx context.Context, id uint64) (Plan, error) {
	var row PlanModel
	if err := e.db.WithContext(ctx).Take(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Plan{}, ErrPlanNotFound
		}
		return Plan{}, err
	}
	ents, err := e.entitlementMap(ctx, []uint64{row.ID})
	if err != nil {
		return Plan{}, err
	}
	return planOf(row, ents[row.ID]), nil
}

// CreatePlan 创建套餐（编码唯一；设置默认档位时清除其他默认）。
func (e *engine) CreatePlan(ctx context.Context, in PlanInput) (Plan, error) {
	if err := validatePlanInput(in); err != nil {
		return Plan{}, err
	}
	var created PlanModel
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&PlanModel{}).Where("code = ?", in.Code).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrPlanCodeExists
		}
		if err := clearOtherDefaultsTx(tx, in.IsDefault, 0); err != nil {
			return err
		}
		row, ents := planModelsFromInput(in, 0, time.Now())
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if len(ents) > 0 {
			if err := tx.Create(&ents).Error; err != nil {
				return err
			}
		}
		created = row
		created.ID = row.ID
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return e.Plan(ctx, created.ID)
}

// UpdatePlan 更新套餐并整体替换权益配置。
func (e *engine) UpdatePlan(ctx context.Context, id uint64, in PlanInput) error {
	if err := validatePlanInput(in); err != nil {
		return err
	}
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row PlanModel
		if err := tx.Take(&row, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPlanNotFound
			}
			return err
		}
		var count int64
		if err := tx.Model(&PlanModel{}).Where("code = ? AND id <> ?", in.Code, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrPlanCodeExists
		}
		if err := clearOtherDefaultsTx(tx, in.IsDefault, id); err != nil {
			return err
		}
		updates := map[string]any{
			"code":                in.Code,
			"name":                in.Name,
			"monthly_price_cents": in.MonthlyPriceCents,
			"yearly_price_cents":  in.YearlyPriceCents,
			"capabilities_json":   capabilitiesJSON(in.Capabilities),
			"is_default":          in.IsDefault,
			"status":              normalizeStatus(in.Status),
			"remark":              in.Remark,
			"meta":                in.Meta,
			"updated_at":          time.Now(),
		}
		if err := tx.Model(&PlanModel{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("plan_id = ?", id).Delete(&PlanEntitlement{}).Error; err != nil {
			return err
		}
		_, ents := planModelsFromInput(in, id, time.Now())
		if len(ents) > 0 {
			return tx.Create(&ents).Error
		}
		return nil
	})
}

// SeedPlans 按编码幂等同步内置套餐目录（存在则更新，缺失则创建）。
func (e *engine) SeedPlans(ctx context.Context, defs []PlanInput) error {
	for _, def := range defs {
		if err := validatePlanInput(def); err != nil {
			return err
		}
	}
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for _, def := range defs {
			var row PlanModel
			err := tx.Take(&row, "code = ?", def.Code).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				planRow, ents := planModelsFromInput(def, 0, now)
				if err := tx.Create(&planRow).Error; err != nil {
					return err
				}
				for i := range ents {
					ents[i].PlanID = planRow.ID
				}
				if len(ents) > 0 {
					if err := tx.Create(&ents).Error; err != nil {
						return err
					}
				}
				continue
			}
			if err != nil {
				return err
			}
			updates := map[string]any{
				"name":                def.Name,
				"monthly_price_cents": def.MonthlyPriceCents,
				"yearly_price_cents":  def.YearlyPriceCents,
				"capabilities_json":   capabilitiesJSON(def.Capabilities),
				"is_default":          def.IsDefault,
				"status":              normalizeStatus(def.Status),
				"remark":              def.Remark,
				"meta":                def.Meta,
				"updated_at":          now,
			}
			if err := tx.Model(&PlanModel{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.Where("plan_id = ?", row.ID).Delete(&PlanEntitlement{}).Error; err != nil {
				return err
			}
			_, ents := planModelsFromInput(def, row.ID, now)
			if len(ents) > 0 {
				if err := tx.Create(&ents).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ── 订阅 ──

// Assign 管理端直接开通套餐（永久有效）。
func (e *engine) Assign(ctx context.Context, tenantID, planID, operatorID uint64) (Subscription, error) {
	if tenantID == 0 || planID == 0 {
		return Subscription{}, ErrInvalidOrder
	}
	_ = operatorID
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row PlanModel
		if err := tx.Take(&row, "id = ?", planID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPlanNotFound
			}
			return err
		}
		if row.Status != 1 {
			return ErrPlanDisabled
		}
		now := time.Now()
		return upsertSubscriptionTx(tx, tenantID, row, now, time.Time{})
	})
	if err != nil {
		return Subscription{}, err
	}
	return e.Subscription(ctx, tenantID)
}

// Subscription 查询当前生效订阅。
func (e *engine) Subscription(ctx context.Context, tenantID uint64) (Subscription, error) {
	var row PlanSubscription
	if err := e.db.WithContext(ctx).Take(&row, "tenant_id = ?", tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Subscription{}, ErrSubscriptionMiss
		}
		return Subscription{}, err
	}
	return subscriptionOf(row), nil
}

// ── 购买订单 ──

// CreatePurchase 创建支付订单；免费套餐走 Assign，不走支付。
func (e *engine) CreatePurchase(ctx context.Context, in PurchaseInput) (Purchase, error) {
	if in.TenantID == 0 || in.PlanID == 0 || strings.TrimSpace(in.OutTradeNo) == "" {
		return Purchase{}, ErrInvalidOrder
	}
	if in.BillingCycle != CycleMonthly && in.BillingCycle != CycleYearly {
		return Purchase{}, ErrInvalidCycle
	}
	var row PlanModel
	if err := e.db.WithContext(ctx).Take(&row, "id = ?", in.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Purchase{}, ErrPlanNotFound
		}
		return Purchase{}, err
	}
	if row.Status != 1 {
		return Purchase{}, ErrPlanDisabled
	}
	amount := PriceFor(planOf(row, nil), in.BillingCycle)
	if amount <= 0 {
		return Purchase{}, ErrFreePlan
	}
	deadline := in.PayDeadline
	if deadline.IsZero() {
		deadline = time.Now().Add(defaultPayDeadline)
	}
	record := PlanPurchase{
		TenantID:     in.TenantID,
		PlanID:       row.ID,
		PlanName:     row.Name,
		BillingCycle: in.BillingCycle,
		AmountCents:  amount,
		OutTradeNo:   strings.TrimSpace(in.OutTradeNo),
		PayDeadline:  deadline,
		Status:       PurchasePending,
		OperatorID:   in.OperatorID,
	}
	if err := e.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Purchase{}, err
	}
	return purchaseOf(record), nil
}

// Purchase 按商户订单号查询。
func (e *engine) Purchase(ctx context.Context, outTradeNo string) (Purchase, error) {
	var row PlanPurchase
	if err := e.db.WithContext(ctx).Take(&row, "out_trade_no = ?", strings.TrimSpace(outTradeNo)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Purchase{}, ErrOrderNotFound
		}
		return Purchase{}, err
	}
	return purchaseOf(row), nil
}

// Purchases 分页查询购买订单。
func (e *engine) Purchases(ctx context.Context, q PurchaseQuery) (PurchasePage, error) {
	page, size := normalizePage(q.Page, q.PageSize)
	query := e.db.WithContext(ctx).Model(&PlanPurchase{})
	if q.TenantID > 0 {
		query = query.Where("tenant_id = ?", q.TenantID)
	}
	if q.Status != nil {
		query = query.Where("status = ?", *q.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PurchasePage{}, err
	}
	var rows []PlanPurchase
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return PurchasePage{}, err
	}
	items := make([]Purchase, 0, len(rows))
	for _, row := range rows {
		items = append(items, purchaseOf(row))
	}
	return PurchasePage{Items: items, Total: total, Page: page, Size: size}, nil
}

// MarkPurchasePaid 支付成功生效：订单置已支付（幂等），同事务内生效订阅——
// 同套餐未到期从当前到期时间顺延，否则从支付时间起算新周期。
func (e *engine) MarkPurchasePaid(ctx context.Context, outTradeNo, transactionID string, payTime time.Time) (Purchase, error) {
	outTradeNo = strings.TrimSpace(outTradeNo)
	var result Purchase
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record PlanPurchase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&record, "out_trade_no = ?", outTradeNo).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		if record.Status == PurchasePaid {
			result = purchaseOf(record)
			return nil
		}
		if record.Status == PurchaseClosed {
			return ErrOrderStateInvalid
		}
		if payTime.IsZero() {
			payTime = time.Now()
		}
		startedAt, expireAt, err := nextWindowTx(tx, record.TenantID, record.PlanID, record.BillingCycle, payTime)
		if err != nil {
			return err
		}
		var planRow PlanModel
		if err := tx.Take(&planRow, "id = ?", record.PlanID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPlanNotFound
			}
			return err
		}
		if err := upsertSubscriptionTx(tx, record.TenantID, planRow, startedAt, expireAt); err != nil {
			return err
		}
		updates := map[string]any{
			"transaction_id": strings.TrimSpace(transactionID),
			"pay_time":       payTime,
			"started_at":     startedAt,
			"expire_at":      expireAt,
			"status":         PurchasePaid,
			"updated_at":     time.Now(),
		}
		if err := tx.Model(&PlanPurchase{}).Where("id = ?", record.ID).Updates(updates).Error; err != nil {
			return err
		}
		record.TransactionID = strings.TrimSpace(transactionID)
		record.PayTime = &payTime
		record.StartedAt = startedAt
		record.ExpireAt = expireAt
		record.Status = PurchasePaid
		result = purchaseOf(record)
		return nil
	})
	if err != nil {
		return Purchase{}, err
	}
	return result, nil
}

// ClosePurchase 关闭待支付订单（幂等：已关闭直接成功，已支付报错）。
func (e *engine) ClosePurchase(ctx context.Context, outTradeNo, reason string) error {
	outTradeNo = strings.TrimSpace(outTradeNo)
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record PlanPurchase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&record, "out_trade_no = ?", outTradeNo).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return err
		}
		if record.Status == PurchasePaid {
			return ErrOrderStateInvalid
		}
		if record.Status == PurchaseClosed {
			return nil
		}
		return tx.Model(&PlanPurchase{}).Where("id = ?", record.ID).Updates(map[string]any{
			"status":     PurchaseClosed,
			"remark":     strings.TrimSpace(reason),
			"updated_at": time.Now(),
		}).Error
	})
}

// ── 权益校验 ──

// EnsureEnabled 校验布尔权益已开通。
func (e *engine) EnsureEnabled(ctx context.Context, tenantID uint64, code, label string) error {
	sub, err := e.Subscription(ctx, tenantID)
	if err != nil {
		return err
	}
	var ent PlanEntitlement
	if err := e.db.WithContext(ctx).Take(&ent, "plan_id = ? AND code = ?", sub.PlanID, code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrEntitlementMiss
		}
		return err
	}
	if ent.ValueType != ValueTypeBoolean {
		return ErrEntitlementMiss
	}
	if !ent.Enabled {
		return &NotEnabledError{Code: code, Label: label}
	}
	return nil
}

// EnforceCount 校验计数额度（无锁，used 由业务侧统计）。
func (e *engine) EnforceCount(ctx context.Context, tenantID uint64, code string, used int64, label string) error {
	sub, err := e.Subscription(ctx, tenantID)
	if err != nil {
		return err
	}
	var ent PlanEntitlement
	if err := e.db.WithContext(ctx).Take(&ent, "plan_id = ? AND code = ?", sub.PlanID, code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrEntitlementMiss
		}
		return err
	}
	return enforceCountEnt(ent, code, label, used)
}

// EnforceCountTx 在业务事务内锁定订阅行后校验计数额度（防并发超限）。
func (e *engine) EnforceCountTx(tx *gorm.DB, tenantID uint64, code string, used int64, label string) error {
	if tx == nil {
		return errors.New("套餐额度校验缺少事务")
	}
	sub, err := lockSubscriptionTx(tx, tenantID)
	if err != nil {
		return err
	}
	var ent PlanEntitlement
	if err := tx.Take(&ent, "plan_id = ? AND code = ?", sub.PlanID, code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrEntitlementMiss
		}
		return err
	}
	return enforceCountEnt(ent, code, label, used)
}

// ConsumeMonthly 原子校验并记录按月计数权益消耗（sourceKey 非空时按来源幂等）。
func (e *engine) ConsumeMonthly(ctx context.Context, tenantID uint64, code string, operatorID uint64, sourceType, sourceKey string) error {
	if tenantID == 0 || code == "" || sourceType == "" {
		return ErrInvalidOrder
	}
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sub, err := lockSubscriptionTx(tx, tenantID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(sourceKey) != "" {
			var existing int64
			if err := tx.Model(&PlanUsageEvent{}).
				Where("tenant_id = ? AND entitlement_code = ? AND source_type = ? AND source_key = ?",
					tenantID, code, sourceType, sourceKey).
				Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				return nil
			}
		}
		var ent PlanEntitlement
		if err := tx.Take(&ent, "plan_id = ? AND code = ?", sub.PlanID, code).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEntitlementMiss
			}
			return err
		}
		now := time.Now()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		var used int64
		if err := tx.Model(&PlanUsageEvent{}).
			Where("tenant_id = ? AND entitlement_code = ? AND created_at >= ? AND created_at < ?",
				tenantID, code, monthStart, monthStart.AddDate(0, 1, 0)).
			Select("COALESCE(SUM(usage_value), 0)").
			Scan(&used).Error; err != nil {
			return err
		}
		if err := enforceCountEnt(ent, code, "", used); err != nil {
			return err
		}
		return tx.Create(&PlanUsageEvent{
			TenantID:        tenantID,
			EntitlementCode: code,
			OperatorID:      operatorID,
			UsageValue:      1,
			SourceType:      sourceType,
			SourceKey:       sourceKey,
			CreatedAt:       now,
		}).Error
	})
}

// ── 内部辅助 ──

// nextWindowTx 计算支付成功后的订阅周期：同套餐未到期从当前到期时间顺延，否则从支付时间起算。
func nextWindowTx(tx *gorm.DB, tenantID, planID uint64, cycle string, payTime time.Time) (time.Time, time.Time, error) {
	if cycle != CycleMonthly && cycle != CycleYearly {
		return time.Time{}, time.Time{}, ErrInvalidCycle
	}
	var sub PlanSubscription
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sub, "tenant_id = ?", tenantID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, time.Time{}, err
	}
	startedAt := payTime
	if err == nil && sub.PlanID == planID && !sub.ExpireAt.IsZero() && sub.ExpireAt.After(payTime) {
		startedAt = sub.ExpireAt
	}
	if cycle == CycleYearly {
		return startedAt, startedAt.AddDate(1, 0, 0), nil
	}
	return startedAt, startedAt.AddDate(0, 1, 0), nil
}

// upsertSubscriptionTx 写回主体当前订阅快照（expireAt 零值 = 永久有效）。
func upsertSubscriptionTx(tx *gorm.DB, tenantID uint64, planRow PlanModel, startedAt, expireAt time.Time) error {
	now := time.Now()
	var sub PlanSubscription
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sub, "tenant_id = ?", tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&PlanSubscription{
			TenantID:  tenantID,
			PlanID:    planRow.ID,
			PlanCode:  planRow.Code,
			PlanName:  planRow.Name,
			StartedAt: startedAt,
			ExpireAt:  expireAt,
			UpdatedAt: now,
		}).Error
	}
	if err != nil {
		return err
	}
	return tx.Model(&PlanSubscription{}).Where("id = ?", sub.ID).Updates(map[string]any{
		"plan_id":    planRow.ID,
		"plan_code":  planRow.Code,
		"plan_name":  planRow.Name,
		"started_at": startedAt,
		"expire_at":  expireAt,
		"updated_at": now,
	}).Error
}

// lockSubscriptionTx 事务内锁定主体订阅行。
func lockSubscriptionTx(tx *gorm.DB, tenantID uint64) (PlanSubscription, error) {
	var sub PlanSubscription
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sub, "tenant_id = ?", tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PlanSubscription{}, ErrSubscriptionMiss
	}
	if err != nil {
		return PlanSubscription{}, err
	}
	return sub, nil
}

// enforceCountEnt 按权益配置校验计数额度。
func enforceCountEnt(ent PlanEntitlement, code, label string, used int64) error {
	if ent.ValueType != ValueTypeCount {
		return ErrEntitlementMiss
	}
	if !ent.Enabled {
		return &QuotaExceededError{Code: code, Label: label, Limit: 0, Used: 0}
	}
	if used >= ent.LimitValue {
		return &QuotaExceededError{Code: code, Label: label, Limit: ent.LimitValue, Used: used}
	}
	return nil
}

// clearOtherDefaultsTx 设置新默认档位时清除其他默认。
func clearOtherDefaultsTx(tx *gorm.DB, isDefault bool, excludeID uint64) error {
	if !isDefault {
		return nil
	}
	query := tx.Model(&PlanModel{}).Where("is_default = ?", true)
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}
	return query.Updates(map[string]any{"is_default": false, "updated_at": time.Now()}).Error
}

// validatePlanInput 校验套餐入参。
func validatePlanInput(in PlanInput) error {
	if strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Name) == "" {
		return ErrInvalidPlan
	}
	if in.MonthlyPriceCents < 0 || in.YearlyPriceCents < 0 {
		return ErrInvalidPlan
	}
	seen := make(map[string]struct{}, len(in.Entitlements))
	for _, ent := range in.Entitlements {
		code := strings.TrimSpace(ent.Code)
		if code == "" {
			return ErrInvalidPlan
		}
		if ent.ValueType != ValueTypeCount && ent.ValueType != ValueTypeBoolean {
			return ErrInvalidPlan
		}
		if ent.ValueType == ValueTypeCount && ent.LimitValue < 0 {
			return ErrInvalidPlan
		}
		if _, dup := seen[code]; dup {
			return ErrInvalidPlan
		}
		seen[code] = struct{}{}
	}
	return nil
}

// planModelsFromInput 将入参转换为持久化模型（planID 为 0 表示待创建）。
func planModelsFromInput(in PlanInput, planID uint64, now time.Time) (PlanModel, []PlanEntitlement) {
	row := PlanModel{
		Code:              strings.TrimSpace(in.Code),
		Name:              strings.TrimSpace(in.Name),
		MonthlyPriceCents: in.MonthlyPriceCents,
		YearlyPriceCents:  in.YearlyPriceCents,
		CapabilitiesJSON:  capabilitiesJSON(in.Capabilities),
		IsDefault:         in.IsDefault,
		Status:            normalizeStatus(in.Status),
		Remark:            in.Remark,
		Meta:              in.Meta,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	row.ID = planID
	ents := make([]PlanEntitlement, 0, len(in.Entitlements))
	for _, item := range in.Entitlements {
		enabled := item.Enabled
		if item.ValueType == ValueTypeCount {
			enabled = true
		}
		ents = append(ents, PlanEntitlement{
			PlanID:     planID,
			Code:       strings.TrimSpace(item.Code),
			ValueType:  item.ValueType,
			LimitValue: item.LimitValue,
			Enabled:    enabled,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
	}
	return row, ents
}

// entitlementMap 批量加载套餐权益（按套餐 ID 分组）。
func (e *engine) entitlementMap(ctx context.Context, ids []uint64) (map[uint64][]PlanEntitlement, error) {
	result := make(map[uint64][]PlanEntitlement)
	if len(ids) == 0 {
		return result, nil
	}
	var rows []PlanEntitlement
	if err := e.db.WithContext(ctx).Where("plan_id IN ?", ids).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.PlanID] = append(result[row.PlanID], row)
	}
	return result, nil
}

func planIDsOf(rows []PlanModel) []uint64 {
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func planOf(row PlanModel, ents []PlanEntitlement) Plan {
	capabilities := make([]string, 0)
	_ = json.Unmarshal([]byte(row.CapabilitiesJSON), &capabilities)
	items := make([]Entitlement, 0, len(ents))
	for _, ent := range ents {
		items = append(items, Entitlement{
			Code:       ent.Code,
			ValueType:  ent.ValueType,
			LimitValue: ent.LimitValue,
			Enabled:    ent.Enabled,
		})
	}
	return Plan{
		ID:                row.ID,
		Code:              row.Code,
		Name:              row.Name,
		MonthlyPriceCents: row.MonthlyPriceCents,
		YearlyPriceCents:  row.YearlyPriceCents,
		Capabilities:      capabilities,
		IsDefault:         row.IsDefault,
		Status:            row.Status,
		Remark:            row.Remark,
		Meta:              row.Meta,
		Entitlements:      items,
	}
}

func subscriptionOf(row PlanSubscription) Subscription {
	return Subscription{
		TenantID:  row.TenantID,
		PlanID:    row.PlanID,
		PlanCode:  row.PlanCode,
		PlanName:  row.PlanName,
		StartedAt: row.StartedAt,
		ExpireAt:  row.ExpireAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func purchaseOf(row PlanPurchase) Purchase {
	return Purchase{
		ID:            row.ID,
		TenantID:      row.TenantID,
		PlanID:        row.PlanID,
		PlanName:      row.PlanName,
		BillingCycle:  row.BillingCycle,
		AmountCents:   row.AmountCents,
		OutTradeNo:    row.OutTradeNo,
		TransactionID: row.TransactionID,
		PayDeadline:   row.PayDeadline,
		StartedAt:     row.StartedAt,
		ExpireAt:      row.ExpireAt,
		PayTime:       row.PayTime,
		Status:        row.Status,
		Remark:        row.Remark,
		Extra:         row.Extra,
		OperatorID:    row.OperatorID,
		CreatedAt:     row.CreatedAt,
	}
}

func capabilitiesJSON(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func normalizeStatus(status int) int {
	if status == 0 {
		return 0
	}
	return 1
}

func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
