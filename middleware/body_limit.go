package middleware

import (
	"context"
	"errors"
	"io"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/response"
)

// errBodyTooLarge 表示请求体读取量超出上限。
var errBodyTooLarge = errors.New("request body exceeds size limit")

// BodyLimit 限制请求体大小，防止超大请求占用内存与带宽。
// 带 Content-Length 的请求在进入业务前直接拒绝；
// 无长度（chunked）请求通过包装请求体流，在业务读取超出上限时返回错误。
func BodyLimit(cfg config.BodyLimitConfig) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if !cfg.Enabled || cfg.MaxBytes <= 0 || string(c.Method()) == "OPTIONS" {
			c.Next(ctx)
			return
		}
		length := c.Request.Header.ContentLength()
		if length > 0 && int64(length) > cfg.MaxBytes {
			response.Error(c, 413, "请求体过大")
			c.Abort()
			return
		}
		// 无 Content-Length 的请求无法预检，包装流让读取在超限时失败。
		if length < 0 {
			if stream := c.Request.BodyStream(); stream != nil {
				c.Request.SetBodyStream(newLimitedBodyReader(stream, cfg.MaxBytes), -1)
			}
		}
		c.Next(ctx)
	}
}

// limitedBodyReader 限制底层流的累计读取量，超出上限时返回 errBodyTooLarge。
type limitedBodyReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

// newLimitedBodyReader 创建带上限的请求体流读取器。
func newLimitedBodyReader(reader io.Reader, limit int64) *limitedBodyReader {
	return &limitedBodyReader{reader: reader, remaining: limit}
}

func (l *limitedBodyReader) Read(p []byte) (int, error) {
	if l.exceeded {
		return 0, errBodyTooLarge
	}
	if l.remaining <= 0 {
		// 上限刚好用完：再探测 1 字节，区分"恰好等于上限"与"超出上限"。
		var probe [1]byte
		n, err := l.reader.Read(probe[:])
		if n > 0 {
			l.exceeded = true
			return 0, errBodyTooLarge
		}
		if err != nil {
			return 0, err
		}
		return 0, nil
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.reader.Read(p)
	l.remaining -= int64(n)
	return n, err
}
