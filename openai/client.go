package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/xinpaiyun/nova-lib/config"
)

const (
	defaultTextModel   = "gpt-4o-mini"
	defaultVisionModel = "gpt-4o-mini"
)

// Client 封装 OpenAI 或兼容协议模型服务调用。
type Client struct {
	cfg    config.AIConfig
	client openaisdk.Client
}

// CompleteTextReq 描述一次文本补全请求。
type CompleteTextReq struct {
	SystemPrompt string
	UserPrompt   string
	Model        string
	Temperature  *float64
	MaxTokens    int64
}

// CompleteVisionReq 描述一次视觉理解请求。
type CompleteVisionReq struct {
	SystemPrompt string
	UserPrompt   string
	ImageURL     string
	ImageDetail  string
	Model        string
	Temperature  *float64
	MaxTokens    int64
}

// CompleteTextResp 描述文本或视觉模型响应。
type CompleteTextResp struct {
	Content          string `json:"content"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
}

// NewClient 创建 OpenAI 兼容协议客户端，未启用时返回可安全持有的空客户端。
func NewClient(cfg config.AIConfig) (*Client, error) {
	cfg = normalizeConfig(cfg)
	if !cfg.Enabled {
		return &Client{cfg: cfg}, nil
	}
	if cfg.APIKey == "" {
		return nil, errors.New("openai api key is required")
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithHTTPClient(&http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second}),
	}
	if cfg.BaseURL != "" {
		if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
			return nil, errors.New("openai base_url is invalid")
		}
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &Client{cfg: cfg, client: openaisdk.NewClient(opts...)}, nil
}

// IsEnabled 判断客户端是否可发起模型请求。
func (c *Client) IsEnabled() bool {
	return c != nil && c.cfg.Enabled && c.cfg.APIKey != ""
}

// DefaultModel 返回当前默认文本模型名称，兼容历史调用。
func (c *Client) DefaultModel() string {
	return c.DefaultTextModel()
}

// DefaultTextModel 返回当前默认文本模型名称。
func (c *Client) DefaultTextModel() string {
	if c == nil || strings.TrimSpace(c.cfg.TextModel) == "" {
		return defaultTextModel
	}
	return c.cfg.TextModel
}

// DefaultVisionModel 返回当前默认视觉模型名称。
func (c *Client) DefaultVisionModel() string {
	if c == nil || strings.TrimSpace(c.cfg.VisionModel) == "" {
		return defaultVisionModel
	}
	return c.cfg.VisionModel
}

// CompleteText 使用 Chat Completions 执行一次文本生成。
func (c *Client) CompleteText(ctx context.Context, req CompleteTextReq) (*CompleteTextResp, error) {
	if !c.IsEnabled() {
		return nil, errors.New("OpenAI 未启用")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultTextModel()
	}
	messages := []openaisdk.ChatCompletionMessageParamUnion{}
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, openaisdk.SystemMessage(strings.TrimSpace(req.SystemPrompt)))
	}
	messages = append(messages, openaisdk.UserMessage(strings.TrimSpace(req.UserPrompt)))
	params := openaisdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: messages,
	}
	if req.Temperature != nil {
		params.Temperature = openaisdk.Float(*req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openaisdk.Int(req.MaxTokens)
	}
	startedAt := time.Now()
	slog.Debug("openai text request", "model", model, "max_tokens", req.MaxTokens, "temperature", req.Temperature)
	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		slog.Error("openai text request failed", "model", model, "elapsed_ms", time.Since(startedAt).Milliseconds(), "error", err.Error())
		return nil, err
	}
	if len(resp.Choices) == 0 {
		slog.Warn("openai text response empty", "model", model, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return nil, errors.New("OpenAI 响应为空")
	}
	content := resp.Choices[0].Message.Content
	slog.Debug("openai text response", "model", resp.Model, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return &CompleteTextResp{
		Content: content, Model: resp.Model,
		PromptTokens: int(resp.Usage.PromptTokens), CompletionTokens: int(resp.Usage.CompletionTokens),
		TotalTokens: int(resp.Usage.TotalTokens),
	}, nil
}

// DataURL 将文件二进制转为视觉模型可读取的 data URL。
func DataURL(mimeType string, data []byte) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// CompleteVision 使用视觉模型基于图片 URL 执行一次理解生成。
func (c *Client) CompleteVision(ctx context.Context, req CompleteVisionReq) (*CompleteTextResp, error) {
	if !c.IsEnabled() {
		return nil, errors.New("OpenAI 未启用")
	}
	imageURL := strings.TrimSpace(req.ImageURL)
	if imageURL == "" {
		return nil, errors.New("图片 URL 不能为空")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultVisionModel()
	}
	detail := strings.TrimSpace(req.ImageDetail)
	if detail == "" {
		detail = "auto"
	}
	messages := []openaisdk.ChatCompletionMessageParamUnion{}
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, openaisdk.SystemMessage(strings.TrimSpace(req.SystemPrompt)))
	}
	messages = append(messages, openaisdk.UserMessage([]openaisdk.ChatCompletionContentPartUnionParam{
		openaisdk.TextContentPart(strings.TrimSpace(req.UserPrompt)),
		openaisdk.ImageContentPart(openaisdk.ChatCompletionContentPartImageImageURLParam{
			URL:    imageURL,
			Detail: detail,
		}),
	}))
	params := openaisdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: messages,
	}
	if req.Temperature != nil {
		params.Temperature = openaisdk.Float(*req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openaisdk.Int(req.MaxTokens)
	}
	startedAt := time.Now()
	slog.Debug("openai vision request", "model", model, "image", summarizeImageURL(imageURL), "image_detail", detail, "max_tokens", req.MaxTokens, "temperature", req.Temperature)
	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		slog.Error("openai vision request failed", "model", model, "elapsed_ms", time.Since(startedAt).Milliseconds(), "error", err.Error())
		return nil, err
	}
	if len(resp.Choices) == 0 {
		slog.Warn("openai vision response empty", "model", model, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return nil, errors.New("OpenAI 响应为空")
	}
	content := resp.Choices[0].Message.Content
	slog.Debug("openai vision response", "model", resp.Model, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return &CompleteTextResp{
		Content: content, Model: resp.Model,
		PromptTokens: int(resp.Usage.PromptTokens), CompletionTokens: int(resp.Usage.CompletionTokens),
		TotalTokens: int(resp.Usage.TotalTokens),
	}, nil
}

// summarizeImageURL 返回图片输入摘要，避免把 base64 图片或签名参数写入日志。
func summarizeImageURL(imageURL string) string {
	if strings.HasPrefix(imageURL, "data:") {
		parts := strings.SplitN(imageURL, ",", 2)
		meta := strings.TrimPrefix(parts[0], "data:")
		if len(parts) == 2 {
			return fmt.Sprintf("data:%s,length=%d", meta, len(parts[1]))
		}
		return fmt.Sprintf("data:%s,length=0", meta)
	}
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return "url:invalid"
	}
	return fmt.Sprintf("url:%s%s", parsed.Host, parsed.Path)
}

// normalizeConfig 归一化 OpenAI 配置默认值。
func normalizeConfig(cfg config.AIConfig) config.AIConfig {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.TextModel = strings.TrimSpace(cfg.TextModel)
	cfg.VisionModel = strings.TrimSpace(cfg.VisionModel)
	if cfg.TextModel == "" {
		cfg.TextModel = cfg.Model
	}
	if cfg.TextModel == "" {
		cfg.TextModel = defaultTextModel
	}
	if cfg.VisionModel == "" {
		cfg.VisionModel = defaultVisionModel
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 60
	}
	return cfg
}
