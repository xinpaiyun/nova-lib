// Package scenario 提供四项目共享的两级经营场景编码常量。
// 编码规则见 nova 仓库 docs/product-architecture/saas-platform-pattern.md 第 4 节：
// 格式 {scenario}.{sub-scene}，全小写中划线分隔；一级为经营大类（scenario_code），
// 二级为细化业态（sub_scene_code），两个字段分开存储，允许只定一级、二级后补。
// 各项目的场景枚举与校验由项目自行定义，本包只保证编码字面量四项目一致；
// 新增业态编码追加常量即可，已废弃编码不复用。
package scenario

import "strings"

// 一级经营场景编码。
const (
	Dining  = "dining"  // 餐饮
	Vehicle = "vehicle" // 车辆
	Pet     = "pet"     // 宠物
	Fresh   = "fresh"   // 生鲜
)

// 二级细化业态编码。
const (
	DiningMilkTea      = "dining.milk-tea"     // 奶茶饮品
	DiningDineIn       = "dining.dine-in"      // 堂食正餐
	VehicleRepair      = "vehicle.repair"      // 修理厂
	VehicleMaintenance = "vehicle.maintenance" // 保养
	VehicleBeauty      = "vehicle.beauty"      // 美容洗车
	PetHospital        = "pet.hospital"        // 宠物医院
	PetGrooming        = "pet.grooming"        // 洗护美容
	PetBoarding        = "pet.boarding"        // 寄养
	FreshECommerce     = "fresh.e-commerce"    // 私域电商
	FreshStore         = "fresh.store"         // 生鲜门店
)

// Split 把二级编码拆成一级 + 二级尾段（如 "dining.milk-tea" → "dining", "milk-tea"）。
// 主体档案把 scenario_code 与 sub_scene_code 分开存储时使用；仅含一级编码时 sub 返回空串。
func Split(code string) (first, sub string, ok bool) {
	if code == "" {
		return "", "", false
	}
	first, sub, found := strings.Cut(code, ".")
	if !found {
		return first, "", first != ""
	}
	return first, sub, true
}
