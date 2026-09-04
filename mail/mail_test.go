package mail

import (
	"strings"
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestSendWithConfigRejectsHeaderInjection 验证邮件头字段拒绝 CRLF 注入。
func TestSendWithConfigRejectsHeaderInjection(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.MailConfig
		message Message
	}{
		{
			name:    "to",
			cfg:     config.MailConfig{},
			message: Message{To: "user@example.com\r\nBcc: attacker@example.com", Subject: "通知", Body: "正文"},
		},
		{
			name:    "subject",
			cfg:     config.MailConfig{},
			message: Message{To: "user@example.com", Subject: "通知\nBcc: attacker@example.com", Body: "正文"},
		},
		{
			name:    "from",
			cfg:     config.MailConfig{Enabled: true, Host: "smtp.example.com", Port: 25, From: "sender@example.com\r\nBcc: attacker@example.com"},
			message: Message{To: "user@example.com", Subject: "通知", Body: "正文"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SendWithConfig(tt.cfg, tt.message)
			if err == nil {
				t.Fatal("SendWithConfig() error = nil, want header validation error")
			}
			if !strings.Contains(err.Error(), "不正确") {
				t.Fatalf("SendWithConfig() error = %v, want invalid field error", err)
			}
		})
	}
}

// TestBuildMessageKeepsBodyNewlines 验证正文换行不会被头部注入校验误伤。
func TestBuildMessageKeepsBodyNewlines(t *testing.T) {
	raw := string(buildMessage(
		config.MailConfig{From: "sender@example.com"},
		Message{To: "user@example.com", Subject: "通知", Body: "第一行\n第二行"},
	))
	if !strings.Contains(raw, "\r\n\r\n第一行\n第二行") {
		t.Fatalf("message body = %q, want original body after header separator", raw)
	}
}
