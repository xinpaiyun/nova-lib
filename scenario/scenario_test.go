package scenario

import (
	"strings"
	"testing"
)

// TestSeedFormat 校验种子表编码格式与归属一致性（两级、全小写、中划线分隔）。
func TestSeedFormat(t *testing.T) {
	for _, s := range subScenes {
		if !strings.Contains(s.Code, ".") {
			t.Errorf("二级编码 %q 缺少一级前缀", s.Code)
		}
		if s.Code != strings.ToLower(s.Code) {
			t.Errorf("二级编码 %q 含大写字符", s.Code)
		}
		if strings.ContainsAny(s.Code, " _") {
			t.Errorf("二级编码 %q 含空格或下划线（应使用中划线）", s.Code)
		}
		first, _, ok := Split(s.Code)
		if !ok || first != s.Scenario {
			t.Errorf("二级编码 %q 前缀 %q 与归属一级 %q 不一致", s.Code, first, s.Scenario)
		}
	}
	seen := make(map[string]bool, len(subScenes))
	for _, s := range subScenes {
		if seen[s.Code] {
			t.Errorf("二级编码 %q 重复", s.Code)
		}
		seen[s.Code] = true
	}
}

// TestScenarioSeeds 校验一级场景种子覆盖全部二级归属且无重复。
func TestScenarioSeeds(t *testing.T) {
	scSet := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		if scSet[s.Code] {
			t.Errorf("一级编码 %q 重复", s.Code)
		}
		scSet[s.Code] = true
	}
	for _, sub := range subScenes {
		if !scSet[sub.Scenario] {
			t.Errorf("二级业态 %q 归属的一级 %q 不在种子表", sub.Code, sub.Scenario)
		}
	}
}

func TestLookupAndValidity(t *testing.T) {
	sub, ok := Lookup(DiningMilkTea)
	if !ok || sub.Name != "奶茶饮品" || sub.Deprecated {
		t.Fatalf("Lookup 结果不正确: %+v ok=%v", sub, ok)
	}
	if !IsValid(DiningMilkTea) || IsValid("dining.unknown") || IsValid("") {
		t.Fatal("IsValid 判断不正确")
	}
	if IsDeprecated(DiningMilkTea) {
		t.Fatal("现存种子不应为废弃状态")
	}
}

func TestSubScenesByScenario(t *testing.T) {
	list := SubScenesByScenario(Vehicle)
	if len(list) != 3 {
		t.Fatalf("vehicle 应有 3 个二级业态: %d", len(list))
	}
	for _, s := range list {
		if s.Scenario != Vehicle {
			t.Fatalf("分组结果混入其他一级: %+v", s)
		}
	}
	if len(SubScenesByScenario("unknown")) != 0 {
		t.Fatal("未知一级应返回空列表")
	}
}

func TestSplit(t *testing.T) {
	if first, sub, ok := Split(DiningMilkTea); !ok || first != Dining || sub != "milk-tea" {
		t.Fatalf("Split 拆分不正确: %q %q %v", first, sub, ok)
	}
	if first, sub, ok := Split(Dining); !ok || first != Dining || sub != "" {
		t.Fatalf("仅一级编码应拆出空二级: %q %q %v", first, sub, ok)
	}
	if _, _, ok := Split(""); ok {
		t.Fatal("空编码应返回 false")
	}
}
