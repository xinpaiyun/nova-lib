package shengwang

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
	accesstoken2 "github.com/xinpaiyun/nova-lib/shengwang/official/accesstoken2"
	"github.com/xinpaiyun/nova-lib/shengwang/official/apaastokenbuilder"
)

const (
	defaultAPIBaseURL            = "https://api.sd-rtn.com"
	defaultRegion                = "CN"
	defaultWebSDKVersion         = "2.9.40"
	defaultTokenExpireSeconds    = 7200
	defaultRoomCloseDelaySeconds = 600
	defaultSTTLanguage           = "zh-CN"
	defaultSTTMaxIdleSeconds     = 60
)

// Client 封装声网灵动课堂服务端能力。
type Client struct {
	cfg        config.ShengwangConfig
	httpClient *http.Client
}

// CreateClassroomRequest 定义创建灵动课堂房间所需参数。
type CreateClassroomRequest struct {
	RoomUUID        string
	RoomName        string
	StartTime       *time.Time
	DurationSeconds int
	Whiteboard      bool
}

// StartSpeechToTextRequest 定义启动声网实时转写所需参数。
type StartSpeechToTextRequest struct {
	Name        string
	ChannelName string
	Language    string
	SubBotUID   string
	PubBotUID   string
}

// SpeechToTextSession 描述声网实时转写任务状态。
type SpeechToTextSession struct {
	AgentID  string
	Status   string
	CreateTS int64
}

// NewClient 创建声网服务端客户端，未启用时返回可安全持有的空客户端。
func NewClient(cfg config.ShengwangConfig) *Client {
	cfg = normalizeConfig(cfg)
	return &Client{cfg: cfg, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// IsEnabled 判断声网能力是否启用。
func (c *Client) IsEnabled() bool {
	return c != nil && c.cfg.Enabled
}

// IsConfigured 判断声网关键配置是否完整。
func (c *Client) IsConfigured() bool {
	return c.IsEnabled() &&
		strings.TrimSpace(c.cfg.AppID) != "" &&
		strings.TrimSpace(c.cfg.AppCertificate) != "" &&
		strings.TrimSpace(c.cfg.Region) != "" &&
		strings.TrimSpace(c.cfg.APIBaseURL) != ""
}

// IsSpeechToTextConfigured 判断实时转写 REST 服务配置是否完整。
func (c *Client) IsSpeechToTextConfigured() bool {
	return c.IsConfigured() &&
		c.cfg.STTEnabled &&
		strings.TrimSpace(c.cfg.CustomerKey) != "" &&
		strings.TrimSpace(c.cfg.CustomerSecret) != ""
}

// AppID 返回前端 SDK 初始化所需的 App ID。
func (c *Client) AppID() string {
	if c == nil {
		return ""
	}
	return c.cfg.AppID
}

// Region 返回灵动课堂区域配置。
func (c *Client) Region() string {
	if c == nil {
		return defaultRegion
	}
	return c.cfg.Region
}

// SDKDomain 返回灵动课堂 SDK 域名配置。
func (c *Client) SDKDomain() string {
	if c == nil {
		return ""
	}
	return c.cfg.SDKDomain
}

// WebSDKVersion 返回 CDN 集成使用的灵动课堂 Web SDK 版本。
func (c *Client) WebSDKVersion() string {
	if c == nil || strings.TrimSpace(c.cfg.WebSDKVersion) == "" {
		return defaultWebSDKVersion
	}
	return c.cfg.WebSDKVersion
}

// TokenExpireSeconds 返回课堂用户 Token 有效秒数。
func (c *Client) TokenExpireSeconds() int {
	if c == nil || c.cfg.TokenExpireSeconds <= 0 {
		return defaultTokenExpireSeconds
	}
	return c.cfg.TokenExpireSeconds
}

// RoomCloseDelaySeconds 返回课堂拖堂关闭秒数。
func (c *Client) RoomCloseDelaySeconds() int {
	if c == nil || c.cfg.RoomCloseDelaySeconds < 0 {
		return defaultRoomCloseDelaySeconds
	}
	return c.cfg.RoomCloseDelaySeconds
}

// STTLanguage 返回实时转写默认识别语种。
func (c *Client) STTLanguage() string {
	if c == nil || strings.TrimSpace(c.cfg.STTLanguage) == "" {
		return defaultSTTLanguage
	}
	return c.cfg.STTLanguage
}

// STTMaxIdleSeconds 返回实时转写空闲自动退出秒数。
func (c *Client) STTMaxIdleSeconds() int {
	if c == nil || c.cfg.STTMaxIdleSeconds <= 0 {
		return defaultSTTMaxIdleSeconds
	}
	return c.cfg.STTMaxIdleSeconds
}

// JoinLinkSecret 返回学生浏览器邀请链接的签名密钥。
func (c *Client) JoinLinkSecret() string {
	if c == nil {
		return ""
	}
	if strings.TrimSpace(c.cfg.JoinLinkSecret) != "" {
		return c.cfg.JoinLinkSecret
	}
	return c.cfg.AppCertificate
}

// BuildRoomUserToken 生成灵动课堂房间用户 Token。
func (c *Client) BuildRoomUserToken(roomUUID string, userUUID string, role int16) (string, time.Time, error) {
	if !c.IsConfigured() {
		return "", time.Time{}, errors.New("声网灵动课堂未配置完成")
	}
	expireSeconds := c.TokenExpireSeconds()
	token, err := apaastokenbuilder.BuildRoomUserToken(c.cfg.AppID, c.cfg.AppCertificate, roomUUID, userUUID, role, uint32(expireSeconds))
	if err != nil {
		return "", time.Time{}, err
	}
	return token, time.Now().Add(time.Duration(expireSeconds) * time.Second), nil
}

// BuildRTCToken 生成声网 RTC 频道用户 Token，供转写机器人加入课堂频道。
func (c *Client) BuildRTCToken(channelName string, uid string) (string, time.Time, error) {
	if !c.IsConfigured() {
		return "", time.Time{}, errors.New("声网 RTC 未配置完成")
	}
	channelName = strings.TrimSpace(channelName)
	uid = strings.TrimSpace(uid)
	if channelName == "" || uid == "" {
		return "", time.Time{}, errors.New("声网 RTC token 参数不能为空")
	}
	expireSeconds := c.TokenExpireSeconds()
	expireAt := uint32(time.Now().Unix() + int64(expireSeconds))
	token := accesstoken2.NewAccessToken(c.cfg.AppID, c.cfg.AppCertificate, uint32(expireSeconds))
	rtc := accesstoken2.NewServiceRtc(channelName, uid)
	rtc.AddPrivilege(accesstoken2.PrivilegeJoinChannel, expireAt)
	rtc.AddPrivilege(accesstoken2.PrivilegePublishAudioStream, expireAt)
	rtc.AddPrivilege(accesstoken2.PrivilegePublishDataStream, expireAt)
	token.AddService(rtc)
	value, err := token.Build()
	if err != nil {
		return "", time.Time{}, err
	}
	return value, time.Unix(int64(expireAt), 0), nil
}

// StartSpeechToText 启动声网实时转写服务并返回任务标识。
func (c *Client) StartSpeechToText(ctx context.Context, req StartSpeechToTextRequest) (*SpeechToTextSession, error) {
	if !c.IsSpeechToTextConfigured() {
		return nil, errors.New("声网实时转写未配置完成")
	}
	channelName := strings.TrimSpace(req.ChannelName)
	name := strings.TrimSpace(req.Name)
	subBotUID := strings.TrimSpace(req.SubBotUID)
	pubBotUID := strings.TrimSpace(req.PubBotUID)
	if channelName == "" || name == "" || subBotUID == "" || pubBotUID == "" {
		return nil, errors.New("声网实时转写参数不能为空")
	}
	subToken, _, err := c.BuildRTCToken(channelName, subBotUID)
	if err != nil {
		return nil, err
	}
	pubToken, _, err := c.BuildRTCToken(channelName, pubBotUID)
	if err != nil {
		return nil, err
	}
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = c.STTLanguage()
	}
	body := map[string]any{
		"name":        name,
		"languages":   []string{language},
		"maxIdleTime": c.STTMaxIdleSeconds(),
		"rtcConfig": map[string]any{
			"channelName": channelName,
			"subBotUid":   subBotUID,
			"subBotToken": subToken,
			"pubBotUid":   pubBotUID,
			"pubBotToken": pubToken,
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/%s/api/speech-to-text/v1/projects/%s/join", strings.TrimRight(c.cfg.APIBaseURL, "/"), strings.ToLower(c.Region()), c.cfg.AppID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", c.basicAuthHeader())
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(resp.Body)
	var apiResp struct {
		AgentID  string `json:"agent_id"`
		Status   string `json:"status"`
		CreateTS int64  `json:"create_ts"`
		Reason   string `json:"reason"`
		Detail   string `json:"detail"`
		Message  string `json:"message"`
	}
	_ = json.Unmarshal(rawResp, &apiResp)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.TrimSpace(apiResp.AgentID) != "" {
		return &SpeechToTextSession{AgentID: strings.TrimSpace(apiResp.AgentID), Status: strings.TrimSpace(apiResp.Status), CreateTS: apiResp.CreateTS}, nil
	}
	return nil, fmt.Errorf("声网实时转写启动失败: status=%d reason=%s detail=%s message=%s", resp.StatusCode, apiResp.Reason, apiResp.Detail, apiResp.Message)
}

// StopSpeechToText 停止声网实时转写服务。
func (c *Client) StopSpeechToText(ctx context.Context, agentID string) (*SpeechToTextSession, error) {
	if !c.IsSpeechToTextConfigured() {
		return nil, errors.New("声网实时转写未配置完成")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("声网实时转写任务 ID 不能为空")
	}
	url := fmt.Sprintf("%s/%s/api/speech-to-text/v1/projects/%s/agents/%s/leave", strings.TrimRight(c.cfg.APIBaseURL, "/"), strings.ToLower(c.Region()), c.cfg.AppID, agentID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", c.basicAuthHeader())
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(resp.Body)
	var apiResp struct {
		AgentID  string `json:"agent_id"`
		Status   string `json:"status"`
		CreateTS int64  `json:"create_ts"`
		Reason   string `json:"reason"`
		Detail   string `json:"detail"`
		Message  string `json:"message"`
	}
	_ = json.Unmarshal(rawResp, &apiResp)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if apiResp.AgentID == "" {
			apiResp.AgentID = agentID
		}
		return &SpeechToTextSession{AgentID: strings.TrimSpace(apiResp.AgentID), Status: strings.TrimSpace(apiResp.Status), CreateTS: apiResp.CreateTS}, nil
	}
	return nil, fmt.Errorf("声网实时转写停止失败: status=%d reason=%s detail=%s message=%s", resp.StatusCode, apiResp.Reason, apiResp.Detail, apiResp.Message)
}

// EnsureOneToOneClassroom 创建或复用 1 对 1 灵动课堂房间。
func (c *Client) EnsureOneToOneClassroom(ctx context.Context, req CreateClassroomRequest) error {
	if !c.IsConfigured() {
		return errors.New("声网灵动课堂未配置完成")
	}
	roomUUID := strings.TrimSpace(req.RoomUUID)
	roomName := strings.TrimSpace(req.RoomName)
	if roomUUID == "" || roomName == "" {
		return errors.New("声网课堂房间参数不能为空")
	}
	appToken, err := apaastokenbuilder.BuildAppToken(c.cfg.AppID, c.cfg.AppCertificate, uint32(c.TokenExpireSeconds()))
	if err != nil {
		return err
	}
	duration := req.DurationSeconds
	if duration <= 0 {
		duration = 3600
	}
	startMillis := time.Now().UnixMilli()
	if req.StartTime != nil && !req.StartTime.IsZero() {
		startMillis = req.StartTime.UnixMilli()
	}
	body := map[string]any{
		"roomName": roomName,
		"roomType": 0,
		"roomProperties": map[string]any{
			"schedule": map[string]any{
				"startTime":  startMillis,
				"duration":   duration,
				"closeDelay": c.RoomCloseDelaySeconds(),
			},
			"widgets": map[string]any{
				"netlessBoard": map[string]any{"state": boolToWidgetState(req.Whiteboard)},
				"easemobIM":    map[string]any{"state": 0},
				"cloudStorage": map[string]any{"state": 1},
			},
		},
		"roleConfig": map[string]any{
			"2": map[string]any{
				"limit": 1,
				"defaultStream": map[string]any{
					"state":      1,
					"videoState": 1,
					"audioState": 1,
				},
			},
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/edu/apps/%s/v2/rooms/%s", strings.TrimRight(c.cfg.APIBaseURL, "/"), c.cfg.Region, c.cfg.AppID, roomUUID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json;charset=UTF-8")
	httpReq.Header.Set("Authorization", fmt.Sprintf("agora token=%s", appToken))
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var apiResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && apiResp.Code == 0 {
		return nil
	}
	if isClassroomAlreadyCreated(resp.StatusCode, apiResp.Code, apiResp.Msg) {
		return nil
	}
	return fmt.Errorf("声网课堂创建失败: status=%d code=%d msg=%s", resp.StatusCode, apiResp.Code, strings.TrimSpace(apiResp.Msg))
}

// basicAuthHeader 生成声网 REST API Basic 认证头。
func (c *Client) basicAuthHeader() string {
	raw := strings.TrimSpace(c.cfg.CustomerKey) + ":" + strings.TrimSpace(c.cfg.CustomerSecret)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

// CloseClassroom 将灵动课堂状态设置为关闭并踢出房间内用户。
func (c *Client) CloseClassroom(ctx context.Context, roomUUID string) error {
	if !c.IsConfigured() {
		return errors.New("声网灵动课堂未配置完成")
	}
	roomUUID = strings.TrimSpace(roomUUID)
	if roomUUID == "" {
		return errors.New("声网课堂房间参数不能为空")
	}
	appToken, err := apaastokenbuilder.BuildAppToken(c.cfg.AppID, c.cfg.AppCertificate, uint32(c.TokenExpireSeconds()))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/edu/apps/%s/v2/rooms/%s/states/3", strings.TrimRight(c.cfg.APIBaseURL, "/"), c.cfg.Region, c.cfg.AppID, roomUUID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", fmt.Sprintf("agora token=%s", appToken))
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var apiResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && apiResp.Code == 0 {
		return nil
	}
	if isClassroomAlreadyClosed(apiResp.Code, apiResp.Msg) {
		return nil
	}
	return fmt.Errorf("声网课堂关闭失败: status=%d code=%d msg=%s", resp.StatusCode, apiResp.Code, strings.TrimSpace(apiResp.Msg))
}

// isClassroomAlreadyCreated 判断声网创建房间响应是否表示可复用的已存在房间。
func isClassroomAlreadyCreated(statusCode int, code int, message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return code == 20409100 ||
		(statusCode == http.StatusConflict && strings.Contains(normalized, "conflict")) ||
		strings.Contains(normalized, "exist") ||
		strings.Contains(message, "已存在")
}

// isClassroomAlreadyClosed 判断声网关闭房间响应是否表示房间已处于结束状态。
func isClassroomAlreadyClosed(code int, message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return code == 20410100 ||
		strings.Contains(normalized, "room is end") ||
		strings.Contains(normalized, "class is end") ||
		strings.Contains(message, "已结束")
}

// normalizeConfig 归一化声网配置默认值。
func normalizeConfig(cfg config.ShengwangConfig) config.ShengwangConfig {
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.AppCertificate = strings.TrimSpace(cfg.AppCertificate)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.APIBaseURL = strings.TrimSpace(cfg.APIBaseURL)
	cfg.SDKDomain = strings.TrimSpace(cfg.SDKDomain)
	cfg.WebSDKVersion = strings.TrimSpace(cfg.WebSDKVersion)
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	if cfg.WebSDKVersion == "" {
		cfg.WebSDKVersion = defaultWebSDKVersion
	}
	if cfg.TokenExpireSeconds <= 0 || cfg.TokenExpireSeconds > 86400 {
		cfg.TokenExpireSeconds = defaultTokenExpireSeconds
	}
	if cfg.RoomCloseDelaySeconds < 0 {
		cfg.RoomCloseDelaySeconds = defaultRoomCloseDelaySeconds
	}
	return cfg
}

// boolToWidgetState 将布尔开关转换为灵动课堂 widget 状态。
func boolToWidgetState(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}
