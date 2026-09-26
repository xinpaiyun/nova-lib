package catalog

import "gorm.io/gorm"

// DropLegacyTables 清理可售卖项目引擎接入前的旧服务项目表（service_items）。
// 车汇星/宠遇记未上线、无存量数据需要保留：两项目旧表同名 service_items，
// lib 统一改用 catalog_items，旧表存在即删除，随后由 AutoMigrate 按统一模型重建。
// 上线后 lib 不再使用旧表名，本函数自动成为空操作，可长期保留。
func DropLegacyTables(db *gorm.DB) error {
	if db.Migrator().HasTable("service_items") {
		return db.Exec("DROP TABLE IF EXISTS service_items").Error
	}
	return nil
}
