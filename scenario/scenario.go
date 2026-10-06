// Package scenario 提供四项目共享的两级经营场景编码常量与种子表。
// 编码规则见 nova 仓库 docs/product-architecture/saas-platform-pattern.md 第 4 节：
// 格式 {scenario}.{sub-scene}，全小写中划线分隔；一级为经营大类（scenario_code），
// 二级为细化业态（sub_scene_code），两个字段分开存储，允许只定一级、二级后补。
// 种子只增不改：废弃编码置 Deprecated=true 保留，不删除、不复用。
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

// Scenario 一级经营场景定义。
type Scenario struct {
	Code       string // 一级编码
	Name       string // 中文名称
	Deprecated bool   // 废弃标记（只增不改，废弃置 true 保留）
}

// SubScene 二级细化业态定义。
type SubScene struct {
	Code       string // 二级编码（含一级前缀）
	Scenario   string // 归属一级编码
	Name       string // 中文名称
	Deprecated bool   // 废弃标记（只增不改，废弃置 true 保留）
}

// scenarios 一级场景种子表。
var scenarios = []Scenario{
	{Code: Dining, Name: "餐饮"},
	{Code: Vehicle, Name: "车辆"},
	{Code: Pet, Name: "宠物"},
	{Code: Fresh, Name: "生鲜"},
}

// subScenes 二级业态种子表（只增不改）。
var subScenes = []SubScene{
	{Code: DiningMilkTea, Scenario: Dining, Name: "奶茶饮品"},
	{Code: DiningDineIn, Scenario: Dining, Name: "堂食正餐"},
	{Code: VehicleRepair, Scenario: Vehicle, Name: "修理厂"},
	{Code: VehicleMaintenance, Scenario: Vehicle, Name: "保养"},
	{Code: VehicleBeauty, Scenario: Vehicle, Name: "美容洗车"},
	{Code: PetHospital, Scenario: Pet, Name: "宠物医院"},
	{Code: PetGrooming, Scenario: Pet, Name: "洗护美容"},
	{Code: PetBoarding, Scenario: Pet, Name: "寄养"},
	{Code: FreshECommerce, Scenario: Fresh, Name: "私域电商"},
	{Code: FreshStore, Scenario: Fresh, Name: "生鲜门店"},
}

// Scenarios 返回全部一级经营场景。
func Scenarios() []Scenario {
	out := make([]Scenario, len(scenarios))
	copy(out, scenarios)
	return out
}

// SubScenes 返回全部二级业态种子。
func SubScenes() []SubScene {
	out := make([]SubScene, len(subScenes))
	copy(out, subScenes)
	return out
}

// SubScenesByScenario 返回指定一级场景下的二级业态（含废弃项，调用方按需过滤）。
func SubScenesByScenario(code string) []SubScene {
	var out []SubScene
	for _, s := range subScenes {
		if s.Scenario == code {
			out = append(out, s)
		}
	}
	return out
}

// Lookup 按二级编码查种子定义（未命中返回 ok=false）。
func Lookup(code string) (SubScene, bool) {
	for _, s := range subScenes {
		if s.Code == code {
			return s, true
		}
	}
	return SubScene{}, false
}

// IsValid 判断二级编码是否在种子表内（含废弃项）。
func IsValid(code string) bool {
	_, ok := Lookup(code)
	return ok
}

// IsDeprecated 判断二级编码是否已废弃（未知编码恒为 false）。
func IsDeprecated(code string) bool {
	s, ok := Lookup(code)
	return ok && s.Deprecated
}

// Split 把二级编码拆成一级 + 二级尾段（如 "dining.milk-tea" → "dining", "milk-tea"）。
// 主体档案把 scenario_code 与 sub_scene_code 分开存储时使用；仅含一级前缀时 sub 返回空串。
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
