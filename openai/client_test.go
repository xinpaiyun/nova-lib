package openai

import (
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestDefaultModelFallback 验证 text/vision/ocr 默认模型的配置与回退链。
func TestDefaultModelFallback(t *testing.T) {
	// 未启用客户端：nil 安全。
	var nilClient *Client
	if nilClient.DefaultOCRModel() != defaultVisionModel {
		t.Fatalf("nil client ocr model = %q", nilClient.DefaultOCRModel())
	}

	// 全空配置：ocr 回退到 vision 默认。
	empty, err := NewClient(config.AIConfig{Enabled: true, APIKey: "k"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if empty.DefaultOCRModel() != empty.DefaultVisionModel() {
		t.Fatalf("empty ocr = %q, want fallback to vision %q", empty.DefaultOCRModel(), empty.DefaultVisionModel())
	}

	// 存量项目形态：OCR 模型配置在 vision_model（无独立 ocr_model）。
	legacy, err := NewClient(config.AIConfig{Enabled: true, APIKey: "k", VisionModel: "qwen3.5-ocr"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if legacy.DefaultOCRModel() != "qwen3.5-ocr" {
		t.Fatalf("legacy ocr = %q, want qwen3.5-ocr", legacy.DefaultOCRModel())
	}

	// 新配置形态：vision 与 ocr 分别指定。
	explicit, err := NewClient(config.AIConfig{Enabled: true, APIKey: "k", VisionModel: "qwen3.7-flash", OCRModel: "qwen3.5-ocr"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if explicit.DefaultVisionModel() != "qwen3.7-flash" {
		t.Fatalf("vision = %q, want qwen3.7-flash", explicit.DefaultVisionModel())
	}
	if explicit.DefaultOCRModel() != "qwen3.5-ocr" {
		t.Fatalf("ocr = %q, want qwen3.5-ocr", explicit.DefaultOCRModel())
	}

	// 空白字符归一化。
	blank, err := NewClient(config.AIConfig{Enabled: true, APIKey: "k", OCRModel: "  "})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if blank.DefaultOCRModel() != blank.DefaultVisionModel() {
		t.Fatalf("blank ocr should fall back to vision")
	}
}

// TestCompleteImageValidation 验证图片请求的启用与入参校验（不发起网络请求）。
func TestCompleteImageValidation(t *testing.T) {
	disabled, err := NewClient(config.AIConfig{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	for name, call := range map[string]func() error{
		"vision": func() error { _, err := disabled.CompleteVision(t.Context(), CompleteVisionReq{ImageURL: "https://e/x.png"}); return err },
		"ocr":    func() error { _, err := disabled.CompleteOCR(t.Context(), CompleteVisionReq{ImageURL: "https://e/x.png"}); return err },
	} {
		if err := call(); err == nil || err.Error() != "OpenAI 未启用" {
			t.Fatalf("%s disabled client error = %v", name, err)
		}
	}

	enabled, err := NewClient(config.AIConfig{Enabled: true, APIKey: "k"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := enabled.CompleteOCR(t.Context(), CompleteVisionReq{}); err == nil || err.Error() != "图片 URL 不能为空" {
		t.Fatalf("ocr empty image error = %v", err)
	}
}
