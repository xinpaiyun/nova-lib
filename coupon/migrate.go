package coupon

import (
	"gorm.io/gorm"

	"github.com/xinpaiyun/nova-lib/member"
)

// DropLegacyTables 清理参考引擎接入前的优惠券旧结构表。
// 项目均未上线、无存量数据需要保留：旧表以 customer_id/merchant_id/shop_id/
// work_order_id/service_order_id 等历史列为特征，命中即整表删除，随后由
// AutoMigrate 按统一模型重建。上线后统一结构不含这些特征列，本函数自动成为空操作。
func DropLegacyTables(db *gorm.DB) error {
	return member.DropTablesIfColumnsExist(db, map[string][]string{
		"coupon_templates": {"merchant_id", "shop_id"},
		"coupons":          {"customer_id", "merchant_id", "work_order_id", "service_order_id"},
	})
}
