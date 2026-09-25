package plan

import "gorm.io/gorm"

// DropLegacyTables 清理套餐引擎接入前的星学记旧套餐表（tenant_editions 系列）。
// 项目未上线、无存量数据需要保留：表存在即删除，随后由 AutoMigrate 按
// 统一模型（plans/plan_entitlements/plan_purchases/plan_subscriptions/plan_usage_events）重建。
// lib 不再使用这些旧表名，上线后本函数自动成为空操作，可长期保留。
func DropLegacyTables(db *gorm.DB) error {
	for _, table := range []string{
		"tenant_editions",
		"tenant_edition_entitlements",
		"tenant_edition_capabilities",
		"tenant_edition_usage_events",
		"tenant_edition_purchases",
	} {
		if db.Migrator().HasTable(table) {
			if err := db.Exec("DROP TABLE IF EXISTS " + table).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
