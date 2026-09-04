package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"

	"github.com/xinpaiyun/nova-lib/config"
)

var cfg config.MailConfig

// Message 表示一封待发送邮件。
type Message struct {
	To      string
	Subject string
	Body    string
}

// Init 初始化邮件服务配置。
func Init(c config.MailConfig) error {
	cfg = c
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.Host) == "" || strings.TrimSpace(cfg.From) == "" {
		return fmt.Errorf("邮件服务启用时必须配置 SMTP Host 和发件人")
	}
	return nil
}

// Enabled 返回邮件服务是否启用。
func Enabled() bool {
	return EnabledWithConfig(cfg)
}

// EnabledWithConfig 判断指定邮件配置是否可用于发送。
func EnabledWithConfig(c config.MailConfig) bool {
	return c.Enabled && strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

// Send 发送邮件，未启用时只执行开发态校验。
func Send(message Message) error {
	return SendWithConfig(cfg, message)
}

// SendWithConfig 使用指定配置发送邮件，未启用时只执行开发态校验。
func SendWithConfig(c config.MailConfig, message Message) error {
	message.To = strings.TrimSpace(message.To)
	message.Subject = strings.TrimSpace(message.Subject)
	if message.To == "" {
		return fmt.Errorf("请输入收件人")
	}
	if message.Subject == "" {
		return fmt.Errorf("请输入邮件主题")
	}
	if hasMailHeaderBreak(message.To) || hasMailHeaderBreak(message.Subject) {
		return fmt.Errorf("邮件收件人或主题不正确")
	}
	if !EnabledWithConfig(c) {
		return nil
	}
	if hasMailHeaderBreak(c.From) {
		return fmt.Errorf("邮件发件人不正确")
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	raw := buildMessage(c, message)
	if c.Port == 465 {
		return sendWithTLS(c, addr, message.To, raw)
	}
	var auth smtp.Auth
	if c.Username != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}
	return smtp.SendMail(addr, auth, c.From, []string{message.To}, raw)
}

// hasMailHeaderBreak 判断邮件头字段是否包含换行，防止 SMTP Header 注入。
func hasMailHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

// buildMessage 构造 RFC 822 邮件内容。
func buildMessage(c config.MailConfig, message Message) []byte {
	headers := []string{
		"From: " + c.From,
		"To: " + message.To,
		"Subject: " + message.Subject,
		"MIME-Version: 1.0",
		`Content-Type: text/plain; charset="UTF-8"`,
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + message.Body)
}

// sendWithTLS 通过隐式 TLS 连接 SMTP 服务，适配常见 465 端口。
func sendWithTLS(c config.MailConfig, addr string, to string, raw []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if c.Username != "" {
		auth := smtp.PlainAuth("", c.Username, c.Password, c.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(c.From); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// DialCheck 验证 SMTP 主机是否可连接。
func DialCheck() error {
	if !Enabled() {
		return nil
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)))
	if err != nil {
		return err
	}
	return conn.Close()
}
