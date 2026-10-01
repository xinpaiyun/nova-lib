package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	openaisdk "github.com/openai/openai-go"

	"github.com/xinpaiyun/nova-lib/aiobs"
)

const (
	// defaultASRModel 内置默认语音转文字模型；阿里云百炼兼容模式生产环境建议配置 asr_model: qwen3-asr-flash。
	defaultASRModel = "gpt-4o-mini-transcribe"
	// defaultTTSModel 内置默认文字转语音模型；阿里云百炼兼容模式生产环境建议配置 tts_model: qwen-tts。
	defaultTTSModel = "tts-1"
	// defaultTTSVoice 内置默认合成音色。
	defaultTTSVoice = "alloy"
	// maxSpeakTextRunes 一次语音合成的文本长度上限（rune），防止误用产生超额费用。
	maxSpeakTextRunes = 2000
)

// TranscribeReq 描述一次语音转文字请求。
type TranscribeReq struct {
	// Audio 音频输入流（wav/mp3/m4a/webm 等格式，由 ASR 模型能力决定）。
	Audio io.Reader
	// Filename 音频文件名，SDK 依此推断 multipart 文件名与扩展名。
	Filename string
	Model    string
	// Scenario 业务场景标识，用于调用记录（ai_call_record）与指标区分，如 ai_interview_transcribe。
	Scenario string
}

// TranscribeResp 描述语音转文字响应。
type TranscribeResp struct {
	Text  string
	Model string
}

// SpeakReq 描述一次文字转语音请求。
type SpeakReq struct {
	Text  string
	Model string
	Voice string
	// Scenario 业务场景标识，用于调用记录（ai_call_record）与指标区分，如 ai_interview_speak。
	Scenario string
}

// DefaultASRModel 返回当前默认语音转文字模型名称。
func (c *Client) DefaultASRModel() string {
	if c == nil || strings.TrimSpace(c.cfg.ASRModel) == "" {
		return defaultASRModel
	}
	return c.cfg.ASRModel
}

// DefaultTTSModel 返回当前默认文字转语音模型名称。
func (c *Client) DefaultTTSModel() string {
	if c == nil || strings.TrimSpace(c.cfg.TTSModel) == "" {
		return defaultTTSModel
	}
	return c.cfg.TTSModel
}

// DefaultTTSVoice 返回当前默认合成音色。
func (c *Client) DefaultTTSVoice() string {
	if c == nil || strings.TrimSpace(c.cfg.TTSVoice) == "" {
		return defaultTTSVoice
	}
	return c.cfg.TTSVoice
}

// Transcribe 使用 ASR 模型把音频转写为文字（OpenAI 兼容 /audio/transcriptions）。
func (c *Client) Transcribe(ctx context.Context, req TranscribeReq) (*TranscribeResp, error) {
	if !c.IsEnabled() {
		return nil, errors.New("OpenAI 未启用")
	}
	if req.Audio == nil {
		return nil, errors.New("音频内容不能为空")
	}
	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = "audio.webm"
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultASRModel()
	}
	params := openaisdk.AudioTranscriptionNewParams{
		Model: openaisdk.AudioModel(model),
		File:  openaisdk.File(req.Audio, filename, "application/octet-stream"),
	}
	startedAt := time.Now()
	slog.Debug("openai asr request", "model", model, "filename", filename)
	resp, err := c.client.Audio.Transcriptions.New(ctx, params)
	if err != nil {
		aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindASR, Model: model,
			Outcome: aiobs.OutcomeFailed, Error: err.Error(), ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
		return nil, errors.New("语音识别失败，请稍后重试")
	}
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindASR, Model: model,
			Outcome: aiobs.OutcomeEmpty, ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
		return nil, errors.New("没有识别到语音内容")
	}
	aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindASR, Model: model,
		Outcome: aiobs.OutcomeSuccess, ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
	slog.Debug("openai asr response", "model", model, "elapsed_ms", time.Since(startedAt).Milliseconds(), "text_len", len(text))
	return &TranscribeResp{Text: text, Model: model}, nil
}

// Speak 使用 TTS 模型把文字合成为 mp3 音频二进制（OpenAI 兼容 /audio/speech）。
// 兼容两类响应：标准二进制音频流直接返回；部分兼容网关（如阿里云百炼 qwen-tts）
// 返回 JSON 包裹的音频 URL（data.audio.url），此时解析后拉取 URL 内容返回。
func (c *Client) Speak(ctx context.Context, req SpeakReq) ([]byte, error) {
	if !c.IsEnabled() {
		return nil, errors.New("OpenAI 未启用")
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, errors.New("合成文本不能为空")
	}
	if len([]rune(text)) > maxSpeakTextRunes {
		return nil, errors.New("合成文本过长")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultTTSModel()
	}
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = c.DefaultTTSVoice()
	}
	params := openaisdk.AudioSpeechNewParams{
		Model:          openaisdk.SpeechModel(model),
		Input:          text,
		Voice:          openaisdk.AudioSpeechNewParamsVoice(voice),
		ResponseFormat: openaisdk.AudioSpeechNewParamsResponseFormat("mp3"),
	}
	startedAt := time.Now()
	slog.Debug("openai tts request", "model", model, "voice", voice, "text_len", len(text))
	resp, err := c.client.Audio.Speech.New(ctx, params)
	if err != nil {
		aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindTTS, Model: model,
			Outcome: aiobs.OutcomeFailed, Error: err.Error(), ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
		return nil, errors.New("语音合成失败，请稍后重试")
	}
	defer resp.Body.Close()
	var audio []byte
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		downloader := &http.Client{Timeout: time.Duration(c.cfg.TimeoutSec) * time.Second}
		audio, err = speakAudioFromJSON(ctx, resp.Body, downloader)
		if err != nil {
			aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindTTS, Model: model,
				Outcome: aiobs.OutcomeFailed, Error: err.Error(), ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
			return nil, errors.New("语音合成失败，请稍后重试")
		}
	} else {
		audio, err = io.ReadAll(resp.Body)
		if err != nil {
			aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindTTS, Model: model,
				Outcome: aiobs.OutcomeFailed, Error: err.Error(), ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
			return nil, errors.New("语音合成失败，请稍后重试")
		}
	}
	if len(audio) == 0 {
		aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindTTS, Model: model,
			Outcome: aiobs.OutcomeEmpty, ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
		return nil, errors.New("语音合成结果为空")
	}
	aiobs.Observe(aiobs.Record{Scenario: req.Scenario, Kind: aiobs.KindTTS, Model: model,
		Outcome: aiobs.OutcomeSuccess, ElapsedMs: time.Since(startedAt).Milliseconds(), StartedAt: startedAt})
	slog.Debug("openai tts response", "model", model, "elapsed_ms", time.Since(startedAt).Milliseconds(), "audio_bytes", len(audio))
	return audio, nil
}

// speakAudioFromJSON 从 JSON 包裹的 TTS 响应中提取音频 URL 并拉取音频内容，
// 兼容 data.audio.url / audio.url / url 三层结构（阿里云百炼 qwen-tts 等）。
func speakAudioFromJSON(ctx context.Context, body io.Reader, client *http.Client) ([]byte, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data *struct {
			Audio *struct {
				URL string `json:"url"`
			} `json:"audio"`
		} `json:"data"`
		Audio *struct {
			URL string `json:"url"`
		} `json:"audio"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	audioURL := payload.URL
	if payload.Audio != nil && payload.Audio.URL != "" {
		audioURL = payload.Audio.URL
	}
	if payload.Data != nil && payload.Data.Audio != nil && payload.Data.Audio.URL != "" {
		audioURL = payload.Data.Audio.URL
	}
	if audioURL == "" {
		return nil, errors.New("tts 响应缺少音频 url")
	}
	download, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(download)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts 音频下载失败: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
