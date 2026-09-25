package member

import "gorm.io/gorm"

// DropLegacyTables 清理参考引擎接入前的会员旧结构表。
// 项目均未上线、无存量数据需要保留：旧表以 customer_id/merchant_id/shop_id 等
// 历史隔离/关联列为特征，命中即整表删除，随后由 AutoMigrate 按统一模型重建。
// 上线后统一结构不含这些特征列，本函数自动成为空操作，可长期保留。
func DropLegacyTables(db *gorm.DB) error {
	return DropTablesIfColumnsExist(db, map[string][]string{
		"member_accounts":     {"customer_id", "merchant_id", "shop_id"},
		"member_transactions": {"customer_id", "merchant_id"},
	})
}

// DropTablesIfColumnsExist 删除含任一特征列的表。表名来自 lib 内常量，无注入风险。
func DropTablesIfColumnsExist(db *gorm.DB, specs map[string][]string) error {
	for table, columns := range specs {
		for _, col := range columns {
			var count int64
			if err := db.Raw(
				"SELECT COUNT(*) FROM information_schema.COLUMNS "+
					"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
				table, col,
			).Scan(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				if err := db.Exec("DROP TABLE IF EXISTS " + table).Error; err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
