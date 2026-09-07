package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestBodyLimitRejectsLargeContentLength 验证带 Content-Length 的超限请求直接拒绝。
func TestBodyLimitRejectsLargeContentLength(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.SetContentLength(2048)
	reached := false
	c.SetHandlers(app.HandlersChain{
		BodyLimit(config.BodyLimitConfig{Enabled: true, MaxBytes: 1024}),
		func(_ context.Context, _ *app.RequestContext) { reached = true },
	})
	c.Next(context.Background())

	if reached {
		t.Fatalf("oversized request should not reach next handler")
	}
	if c.Response.StatusCode() != 413 {
		t.Fatalf("status = %d, want 413", c.Response.StatusCode())
	}
	if !c.IsAborted() {
		t.Fatalf("request should be aborted")
	}
}

// TestBodyLimitAllowsContentLengthUnderLimit 验证未超限请求可正常读取请求体。
func TestBodyLimitAllowsContentLengthUnderLimit(t *testing.T) {
	c := app.NewContext(0)
	c.Request.AppendBodyString("hello")
	var body []byte
	var err error
	c.SetHandlers(app.HandlersChain{
		BodyLimit(config.BodyLimitConfig{Enabled: true, MaxBytes: 1024}),
		func(_ context.Context, rc *app.RequestContext) {
			body, err = rc.Request.BodyE()
		},
	})
	c.Next(context.Background())

	if err != nil {
		t.Fatalf("BodyE() error = %v, want nil", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
}

// TestBodyLimitAllowsExactLimit 验证恰好等于上限的请求体可完整读取。
func TestBodyLimitAllowsExactLimit(t *testing.T) {
	c := app.NewContext(0)
	payload := strings.Repeat("a", 1024)
	c.Request.SetBodyStream(strings.NewReader(payload), -1)
	var body []byte
	var err error
	c.SetHandlers(app.HandlersChain{
		BodyLimit(config.BodyLimitConfig{Enabled: true, MaxBytes: 1024}),
		func(_ context.Context, rc *app.RequestContext) {
			body, err = rc.Request.BodyE()
		},
	})
	c.Next(context.Background())

	if err != nil {
		t.Fatalf("BodyE() error = %v, want nil", err)
	}
	if string(body) != payload {
		t.Fatalf("body length = %d, want %d", len(body), len(payload))
	}
}

// TestBodyLimitStreamReadExceedsLimit 验证无 Content-Length 请求在读取超限时返回错误。
func TestBodyLimitStreamReadExceedsLimit(t *testing.T) {
	c := app.NewContext(0)
	c.Request.SetBodyStream(strings.NewReader(strings.Repeat("a", 2048)), -1)
	reached := false
	var err error
	c.SetHandlers(app.HandlersChain{
		BodyLimit(config.BodyLimitConfig{Enabled: true, MaxBytes: 1024}),
		func(_ context.Context, rc *app.RequestContext) {
			reached = true
			_, err = rc.Request.BodyE()
		},
	})
	c.Next(context.Background())

	if !reached {
		t.Fatalf("chunked request should reach next handler, rejection happens on read")
	}
	if err == nil {
		t.Fatalf("BodyE() should fail when exceeding limit")
	}
}

// TestBodyLimitDisabled 验证未启用时不做任何限制。
func TestBodyLimitDisabled(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.SetContentLength(4096)
	reached := false
	c.SetHandlers(app.HandlersChain{
		BodyLimit(config.BodyLimitConfig{Enabled: false, MaxBytes: 1}),
		func(_ context.Context, _ *app.RequestContext) { reached = true },
	})
	c.Next(context.Background())

	if !reached {
		t.Fatalf("disabled BodyLimit should not reject requests")
	}
	if c.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200", c.Response.StatusCode())
	}
}
