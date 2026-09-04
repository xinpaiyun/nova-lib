package ocr

import (
	"testing"
)

// TestParseIDCardData 验证身份证 OCR 多路径字段解析。
func TestParseIDCardData(t *testing.T) {
	data := `{"data":{"face":{"name":"张三","idNumber":"110101199001011234","address":"北京市"}}}`
	result := parseIDCardData("req-1", data)
	if result.Name != "张三" {
		t.Fatalf("Name = %q", result.Name)
	}
	if result.IDNumber != "110101199001011234" {
		t.Fatalf("IDNumber = %q", result.IDNumber)
	}
	if result.RequestID != "req-1" {
		t.Fatalf("RequestID = %q", result.RequestID)
	}
}

// TestParseGeneralTextData 验证通用文字识别递归收集文本。
func TestParseGeneralTextData(t *testing.T) {
	data := `{"prism_wordsInfo":[{"word":" hello "},{"word":"world"}]}`
	result := parseGeneralTextData("req-2", data)
	if result.Text != "hello\nworld" {
		t.Fatalf("Text = %q", result.Text)
	}
}

// TestParseVehicleLicenseData 验证行驶证关键字段解析与完整性判断。
func TestParseVehicleLicenseData(t *testing.T) {
	data := `{"data":{"plateNumber":"京A12345","vin":"LSVAA1234567890"}}`
	result := parseVehicleLicenseData("req-3", data)
	if result.PlateNumber != "京A12345" || result.VIN != "LSVAA1234567890" {
		t.Fatalf("parse result = %+v", result)
	}
	if !hasVehicleLicensePrimaryFields(result) {
		t.Fatal("expected primary fields present")
	}
	if hasVehicleLicensePrimaryFields(&VehicleLicenseResult{}) {
		t.Fatal("expected empty result flagged")
	}
}
