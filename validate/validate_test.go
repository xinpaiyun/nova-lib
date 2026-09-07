package validate

import (
	"strings"
	"testing"
)

type nestedInner struct {
	City string `json:"city" validate:"required"`
}

type sampleReq struct {
	Name     string       `json:"name" validate:"required"`
	Phone    string       `json:"phone" validate:"required"`
	Age      int          `json:"age" validate:"required,min=1,max=120"`
	Amount   float64      `json:"amount" validate:"gt=0"`
	PlanType string       `json:"plan_type" validate:"required,oneof=month quarter year"`
	Status   int          `json:"status" validate:"oneof=0 1"`
	Tags     []string     `json:"tags" validate:"required,min=1,max=3"`
	Inner    nestedInner  `json:"inner"`
	Remark   string       `json:"remark" validate:"-"`
	Optional *string      `json:"optional"`
}

func TestStructPass(t *testing.T) {
	req := sampleReq{
		Name:     "张三",
		Phone:    "13800138000",
		Age:      30,
		Amount:   9.9,
		PlanType: "month",
		Status:   1,
		Tags:     []string{"a"},
		Inner:    nestedInner{City: "北京"},
	}
	if err := Struct(&req); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestStructRequired(t *testing.T) {
	req := sampleReq{Amount: 1, PlanType: "month", Status: 1, Tags: []string{"a"}, Inner: nestedInner{City: "北京"}}
	err := Struct(&req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "name is required" {
		t.Fatalf("unexpected message: %s", got)
	}
}

func TestStructMinOnInt(t *testing.T) {
	req := sampleReq{Name: "n", Phone: "p", Age: 0, Amount: 1, PlanType: "month", Status: 1, Tags: []string{"a"}, Inner: nestedInner{City: "c"}}
	// Age 为 0 时先命中 required（零值即视为缺失）。
	err := Struct(&req)
	if err == nil || err.Error() != "age is required" {
		t.Fatalf("unexpected error: %v", err)
	}

	req.Age = 200
	err = Struct(&req)
	if err == nil || err.Error() != "age must be at most 120" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStructGt(t *testing.T) {
	req := sampleReq{Name: "n", Phone: "p", Age: 1, Amount: 0, PlanType: "month", Status: 1, Tags: []string{"a"}, Inner: nestedInner{City: "c"}}
	err := Struct(&req)
	if err == nil || err.Error() != "amount must be greater than 0" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStructOneofString(t *testing.T) {
	req := sampleReq{Name: "n", Phone: "p", Age: 1, Amount: 1, PlanType: "week", Status: 1, Tags: []string{"a"}, Inner: nestedInner{City: "c"}}
	err := Struct(&req)
	if err == nil || err.Error() != "plan_type must be one of month quarter year" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStructOneofInt(t *testing.T) {
	req := sampleReq{Name: "n", Phone: "p", Age: 1, Amount: 1, PlanType: "month", Status: 2, Tags: []string{"a"}, Inner: nestedInner{City: "c"}}
	err := Struct(&req)
	if err == nil || err.Error() != "status must be one of 0 1" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStructSliceMinMax(t *testing.T) {
	base := sampleReq{Name: "n", Phone: "p", Age: 1, Amount: 1, PlanType: "month", Status: 1, Inner: nestedInner{City: "c"}}

	// nil 切片命中 required。
	base.Tags = nil
	if err := Struct(&base); err == nil || err.Error() != "tags is required" {
		t.Fatalf("unexpected error: %v", err)
	}

	// 空切片通过 required（仅判 nil），由 min=1 拦截。
	base.Tags = []string{}
	if err := Struct(&base); err == nil || err.Error() != "tags must be at least 1" {
		t.Fatalf("unexpected error: %v", err)
	}

	base.Tags = []string{"a", "b", "c", "d"}
	if err := Struct(&base); err == nil || err.Error() != "tags must be at most 3" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStructNested(t *testing.T) {
	req := sampleReq{Name: "n", Phone: "p", Age: 1, Amount: 1, PlanType: "month", Status: 1, Tags: []string{"a"}}
	err := Struct(&req)
	if err == nil || err.Error() != "city is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStructSkipDashAndOptionalPtr(t *testing.T) {
	optional := "hello"
	req := sampleReq{Name: "n", Phone: "p", Age: 1, Amount: 1, PlanType: "month", Status: 1, Tags: []string{"a"}, Remark: "anything", Optional: &optional, Inner: nestedInner{City: "c"}}
	if err := Struct(&req); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestStructNilAndNonStruct(t *testing.T) {
	if err := Struct(nil); err != nil {
		t.Fatalf("nil should pass, got %v", err)
	}
	if err := Struct("not a struct"); err != nil {
		t.Fatalf("non-struct should pass, got %v", err)
	}
	var req *sampleReq
	if err := Struct(req); err != nil {
		t.Fatalf("nil pointer should pass, got %v", err)
	}
}

func TestMessageDefaultRule(t *testing.T) {
	type emailReq struct {
		Mail string `json:"mail" validate:"email"`
	}
	req := emailReq{Mail: "not-an-email"}
	err := Struct(&req)
	if err == nil || !strings.Contains(err.Error(), "mail") || !strings.Contains(err.Error(), "email") {
		t.Fatalf("unexpected error: %v", err)
	}
}
