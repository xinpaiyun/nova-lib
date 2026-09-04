// Package alipay 提供支付宝小程序开放平台标准能力封装：
// OAuth 登录、手机号解密、JSAPI 交易创建/查询与异步通知验签。
package alipay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
)

const defaultGatewayURL = "https://openapi.alipay.com/gateway.do"

// Client 封装支付宝小程序服务端 API 调用。
type Client struct {
	cfg        config.AlipayConfig
	httpClient *http.Client
}

// OAuthTokenResponse 定义支付宝 OAuth 换 token 响应。
type OAuthTokenResponse struct {
	UserID string `json:"user_id"`
	OpenID string `json:"open_id"`
}

// PhoneNumberResponse 定义支付宝手机号查询响应。
type PhoneNumberResponse struct {
	Code    string `json:"code"`
	Msg     string `json:"msg"`
	SubCode string `json:"sub_code"`
	SubMsg  string `json:"sub_msg"`
	Mobile  string `json:"mobile"`
}

// MiniProgramTradeCreateRequest 定义支付宝小程序创建支付交易请求。
type MiniProgramTradeCreateRequest struct {
	OutTradeNo  string
	Description string
	AmountCents int64
	BuyerID     string
	NotifyURL   string
}

// TradeCreateResponse 定义支付宝交易创建响应。
type TradeCreateResponse struct {
	Code       string `json:"code"`
	Msg        string `json:"msg"`
	SubCode    string `json:"sub_code"`
	SubMsg     string `json:"sub_msg"`
	OutTradeNo string `json:"out_trade_no"`
	TradeNo    string `json:"trade_no"`
}

// TradeQueryResponse 定义支付宝交易查询响应。
type TradeQueryResponse struct {
	Code        string `json:"code"`
	Msg         string `json:"msg"`
	SubCode     string `json:"sub_code"`
	SubMsg      string `json:"sub_msg"`
	OutTradeNo  string `json:"out_trade_no"`
	TradeNo     string `json:"trade_no"`
	TradeStatus string `json:"trade_status"`
	BuyerUserID string `json:"buyer_user_id"`
	BuyerOpenID string `json:"buyer_open_id"`
	TotalAmount string `json:"total_amount"`
}

// TradeNotifyResult 定义支付宝支付异步通知解析结果。
type TradeNotifyResult struct {
	AppID       string
	OutTradeNo  string
	TradeNo     string
	TradeStatus string
	BuyerID     string
	NotifyID    string
}

type oauthTokenEnvelope struct {
	Response OAuthTokenResponse `json:"alipay_system_oauth_token_response"`
	Sign     string             `json:"sign"`
}

type phoneNumberEnvelope struct {
	Response PhoneNumberResponse `json:"alipay_open_app_mini_user_phone_query_response"`
	Sign     string              `json:"sign"`
}

type tradeCreateEnvelope struct {
	Response TradeCreateResponse `json:"alipay_trade_create_response"`
	Sign     string              `json:"sign"`
}

type tradeQueryEnvelope struct {
	Response TradeQueryResponse `json:"alipay_trade_query_response"`
	Sign     string              `json:"sign"`
}

type miniPhonePayload struct {
	Response    string `json:"response"`
	Sign        string `json:"sign"`
	SignType    string `json:"sign_type"`
	EncryptType string `json:"encrypt_type"`
	Charset     string `json:"charset"`
}

// NewClient 创建支付宝开放平台客户端。
func NewClient(cfg config.AlipayConfig) *Client {
	return &Client{cfg: cfg, httpClient: &http.Client{Timeout: 15 * time.Second}}
}

// Config 返回支付宝客户端配置，便于业务层读取应用信息。
func (c *Client) Config() config.AlipayConfig {
	return c.cfg
}

// Enabled 返回支付宝客户端是否具备最小可用配置。
func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.cfg.AppID) != ""
}

// SystemOAuthToken 使用小程序 authCode 换取支付宝用户标识。
func (c *Client) SystemOAuthToken(ctx context.Context, authCode string) (*OAuthTokenResponse, error) {
	if strings.TrimSpace(authCode) == "" {
		return nil, errors.New("支付宝授权码不能为空")
	}
	params := c.baseParams("alipay.system.oauth.token")
	params.Set("grant_type", "authorization_code")
	params.Set("code", strings.TrimSpace(authCode))
	if err := c.sign(params); err != nil {
		return nil, err
	}
	body, err := c.executeFormRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	var out oauthTokenEnvelope
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析支付宝授权响应失败: %w, body=%s", err, bodySnippet(body))
	}
	if strings.TrimSpace(out.Response.UserID) == "" && strings.TrimSpace(out.Response.OpenID) == "" {
		return nil, errors.New("支付宝未返回用户标识")
	}
	return &out.Response, nil
}

// QueryMiniUserPhone 根据小程序手机号授权报文获取用户手机号。
// 配置了 EncryptKey 时走本地验签解密，否则调用开放平台查询接口。
func (c *Client) QueryMiniUserPhone(ctx context.Context, encryptedData string) (string, error) {
	if strings.TrimSpace(encryptedData) == "" {
		return "", errors.New("支付宝手机号授权密文不能为空")
	}
	if strings.TrimSpace(c.cfg.EncryptKey) != "" {
		return c.decryptMiniUserPhone(encryptedData)
	}
	normalizedEncryptedData := normalizePhoneEncryptedData(encryptedData)
	bizContent, err := json.Marshal(map[string]string{
		"encrypted_data": normalizedEncryptedData,
	})
	if err != nil {
		return "", err
	}
	params := c.baseParams("alipay.open.app.mini.user.phone.query")
	params.Set("biz_content", string(bizContent))
	if err := c.sign(params); err != nil {
		return "", err
	}
	body, err := c.executeFormRequest(ctx, params)
	if err != nil {
		return "", err
	}
	var out phoneNumberEnvelope
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("解析支付宝手机号响应失败: %w, body=%s", err, bodySnippet(body))
	}
	if out.Response.Code != "" && out.Response.Code != "10000" {
		return "", alipayResponseError(out.Response.Code, out.Response.SubMsg, out.Response.Msg, "支付宝手机号查询失败")
	}
	if strings.TrimSpace(out.Response.Mobile) == "" {
		return "", errors.New("支付宝未返回手机号")
	}
	return strings.TrimSpace(out.Response.Mobile), nil
}

// CreateMiniProgramTrade 创建支付宝小程序支付交易，并返回 tradeNo 供小程序拉起收银台。
func (c *Client) CreateMiniProgramTrade(ctx context.Context, req MiniProgramTradeCreateRequest) (*TradeCreateResponse, error) {
	if strings.TrimSpace(req.OutTradeNo) == "" {
		return nil, errors.New("支付宝商户订单号不能为空")
	}
	if req.AmountCents <= 0 {
		return nil, errors.New("支付宝支付金额必须大于 0")
	}
	if strings.TrimSpace(req.BuyerID) == "" {
		return nil, errors.New("支付宝买家标识不能为空")
	}
	bizContent, err := json.Marshal(map[string]any{
		"out_trade_no": req.OutTradeNo,
		"total_amount": centsToYuan(req.AmountCents),
		"subject":      strings.TrimSpace(req.Description),
		"buyer_id":     strings.TrimSpace(req.BuyerID),
	})
	if err != nil {
		return nil, err
	}
	params := c.baseParams("alipay.trade.create")
	params.Set("biz_content", string(bizContent))
	notifyURL := strings.TrimSpace(req.NotifyURL)
	if notifyURL == "" {
		notifyURL = strings.TrimSpace(c.cfg.NotifyURL)
	}
	if notifyURL != "" {
		params.Set("notify_url", notifyURL)
	}
	if err := c.sign(params); err != nil {
		return nil, err
	}
	body, err := c.executeFormRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	var out tradeCreateEnvelope
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析支付宝交易创建响应失败: %w, body=%s", err, bodySnippet(body))
	}
	if out.Response.Code != "10000" {
		return nil, alipayResponseError(out.Response.Code, out.Response.SubMsg, out.Response.Msg, "支付宝交易创建失败")
	}
	if strings.TrimSpace(out.Response.TradeNo) == "" {
		return nil, errors.New("支付宝未返回交易号")
	}
	return &out.Response, nil
}

// QueryTradeByOutTradeNo 根据商户订单号查询支付宝交易状态。
func (c *Client) QueryTradeByOutTradeNo(ctx context.Context, outTradeNo string) (*TradeQueryResponse, error) {
	if strings.TrimSpace(outTradeNo) == "" {
		return nil, errors.New("支付宝商户订单号不能为空")
	}
	bizContent, err := json.Marshal(map[string]any{
		"out_trade_no": strings.TrimSpace(outTradeNo),
	})
	if err != nil {
		return nil, err
	}
	params := c.baseParams("alipay.trade.query")
	params.Set("biz_content", string(bizContent))
	if err := c.sign(params); err != nil {
		return nil, err
	}
	body, err := c.executeFormRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	var out tradeQueryEnvelope
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析支付宝交易查询响应失败: %w, body=%s", err, bodySnippet(body))
	}
	if out.Response.Code != "10000" {
		return nil, alipayResponseError(out.Response.Code, out.Response.SubMsg, out.Response.Msg, "支付宝交易查询失败")
	}
	return &out.Response, nil
}

// ParseTradeNotify 验签并解析支付宝支付异步通知。
func (c *Client) ParseTradeNotify(request *http.Request) (*TradeNotifyResult, error) {
	if request == nil {
		return nil, errors.New("支付宝通知请求不能为空")
	}
	if err := request.ParseForm(); err != nil {
		return nil, fmt.Errorf("解析支付宝通知失败: %w", err)
	}
	form := request.PostForm
	if len(form) == 0 {
		form = request.Form
	}
	appID := strings.TrimSpace(form.Get("app_id"))
	if appID == "" {
		return nil, errors.New("支付宝通知缺少 app_id")
	}
	if configured := strings.TrimSpace(c.cfg.AppID); configured != "" && appID != configured {
		return nil, errors.New("支付宝通知 appid 与配置不匹配")
	}
	if err := c.verifySignature(form); err != nil {
		return nil, err
	}
	result := &TradeNotifyResult{
		AppID:       appID,
		OutTradeNo:  strings.TrimSpace(form.Get("out_trade_no")),
		TradeNo:     strings.TrimSpace(form.Get("trade_no")),
		TradeStatus: strings.TrimSpace(form.Get("trade_status")),
		BuyerID:     strings.TrimSpace(firstNonEmpty(form.Get("buyer_open_id"), form.Get("buyer_id"), form.Get("buyer_user_id"))),
		NotifyID:    strings.TrimSpace(form.Get("notify_id")),
	}
	if result.OutTradeNo == "" {
		return nil, errors.New("支付宝通知缺少商户订单号")
	}
	return result, nil
}

// decryptMiniUserPhone 使用支付宝小程序返回的 response 报文直接验签并解密手机号。
func (c *Client) decryptMiniUserPhone(encryptedData string) (string, error) {
	payload, err := parseMiniPhonePayload(encryptedData)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Response) == "" {
		return "", errors.New("支付宝手机号授权报文缺少 response")
	}
	if err := c.verifyMiniPhonePayload(payload); err != nil {
		return "", err
	}
	plainText := strings.TrimSpace(payload.Response)
	if !strings.HasPrefix(plainText, "{") {
		plainText, err = decryptAESContent(payload.Response, c.cfg.EncryptKey)
		if err != nil {
			return "", err
		}
	}
	var phone PhoneNumberResponse
	if err := json.Unmarshal([]byte(plainText), &phone); err != nil {
		return "", fmt.Errorf("解析支付宝手机号解密结果失败: %w, body=%s", err, bodySnippet([]byte(plainText)))
	}
	if phone.Code != "" && phone.Code != "10000" {
		return "", alipayResponseError(phone.Code, phone.SubMsg, phone.Msg, "支付宝手机号解密失败")
	}
	if strings.TrimSpace(phone.Mobile) == "" {
		return "", errors.New("支付宝未返回手机号")
	}
	return strings.TrimSpace(phone.Mobile), nil
}

// parseMiniPhonePayload 兼容前端传入完整 JSON 字符串或直接 response 密文。
func parseMiniPhonePayload(encryptedData string) (miniPhonePayload, error) {
	text := strings.TrimSpace(encryptedData)
	if text == "" {
		return miniPhonePayload{}, errors.New("支付宝手机号授权密文不能为空")
	}
	if !strings.HasPrefix(text, "{") {
		return miniPhonePayload{Response: text, SignType: "RSA2", EncryptType: "AES", Charset: "UTF-8"}, nil
	}
	var payload miniPhonePayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return miniPhonePayload{}, fmt.Errorf("解析支付宝手机号授权报文失败: %w", err)
	}
	if payload.SignType == "" {
		payload.SignType = "RSA2"
	}
	if payload.EncryptType == "" {
		payload.EncryptType = "AES"
	}
	if payload.Charset == "" {
		payload.Charset = "UTF-8"
	}
	return payload, nil
}

// verifyMiniPhonePayload 校验支付宝小程序手机号授权报文签名。
func (c *Client) verifyMiniPhonePayload(payload miniPhonePayload) error {
	if strings.TrimSpace(payload.Sign) == "" {
		return nil
	}
	publicKey, err := c.publicKey()
	if err != nil {
		return err
	}
	signContent := strings.TrimSpace(payload.Response)
	if signContent != "" && !strings.HasPrefix(signContent, "{") {
		signContent = `"` + signContent + `"`
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload.Sign))
	if err != nil {
		return fmt.Errorf("支付宝手机号报文签名格式不正确: %w", err)
	}
	sum := sha256.Sum256([]byte(signContent))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, sum[:], signature); err != nil {
		return fmt.Errorf("支付宝手机号报文验签失败: %w", err)
	}
	return nil
}

// decryptAESContent 使用支付宝 AES/CBC/PKCS5Padding 规则解密密文。
func decryptAESContent(content string, encryptKey string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptKey))
	if err != nil {
		return "", fmt.Errorf("支付宝 AES 密钥格式不正确: %w", err)
	}
	cipherText, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content))
	if err != nil {
		return "", fmt.Errorf("支付宝手机号密文格式不正确: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化支付宝 AES 解密失败: %w", err)
	}
	if len(cipherText)%block.BlockSize() != 0 {
		return "", errors.New("支付宝手机号密文长度不正确")
	}
	iv := make([]byte, block.BlockSize())
	plainBytes := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plainBytes, cipherText)
	plainBytes, err = pkcs7Unpad(plainBytes, block.BlockSize())
	if err != nil {
		return "", err
	}
	return string(plainBytes), nil
}

// pkcs7Unpad 移除 AES/CBC 解密后的 PKCS5/PKCS7 填充。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("支付宝 AES 明文填充长度不正确")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("支付宝 AES 明文填充内容不正确")
	}
	if !bytes.Equal(data[len(data)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return nil, errors.New("支付宝 AES 明文填充校验失败")
	}
	return data[:len(data)-padding], nil
}

// normalizePhoneEncryptedData 从支付宝手机号授权返回值中提取可提交给开放平台的 response 密文。
func normalizePhoneEncryptedData(encryptedData string) string {
	text := strings.TrimSpace(encryptedData)
	if !strings.HasPrefix(text, "{") {
		return text
	}
	var payload struct {
		Response      string `json:"response"`
		EncryptedData string `json:"encryptedData"`
		Code          string `json:"code"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return text
	}
	if strings.TrimSpace(payload.Response) != "" {
		return strings.TrimSpace(payload.Response)
	}
	if strings.TrimSpace(payload.EncryptedData) != "" {
		return strings.TrimSpace(payload.EncryptedData)
	}
	if strings.TrimSpace(payload.Code) != "" {
		return strings.TrimSpace(payload.Code)
	}
	return text
}

// bodySnippet 返回外部接口异常响应摘要，避免日志过长。
func bodySnippet(data []byte) string {
	text := strings.TrimSpace(string(data))
	if len(text) > 300 {
		return text[:300] + "...(truncated)"
	}
	return text
}

// baseParams 返回支付宝开放平台公共参数。
func (c *Client) baseParams(method string) url.Values {
	params := url.Values{}
	params.Set("app_id", strings.TrimSpace(c.cfg.AppID))
	params.Set("method", method)
	params.Set("format", "JSON")
	params.Set("charset", "utf-8")
	params.Set("sign_type", "RSA2")
	params.Set("timestamp", time.Now().Format("2006-01-02 15:04:05"))
	params.Set("version", "1.0")
	return params
}

// sign 使用 RSA2 对支付宝请求参数签名。
func (c *Client) sign(params url.Values) error {
	if strings.TrimSpace(c.cfg.AppID) == "" {
		return errors.New("支付宝 appid 未配置")
	}
	privateKey, err := c.privateKey()
	if err != nil {
		return err
	}
	content := signContent(params)
	sum := sha256.Sum256([]byte(content))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return fmt.Errorf("支付宝请求签名失败: %w", err)
	}
	params.Set("sign", base64.StdEncoding.EncodeToString(signature))
	return nil
}

// verifySignature 使用支付宝公钥校验返回签名。
func (c *Client) verifySignature(params url.Values) error {
	signatureText := strings.TrimSpace(params.Get("sign"))
	if signatureText == "" {
		return errors.New("支付宝通知缺少签名")
	}
	publicKey, err := c.publicKey()
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(signatureText)
	if err != nil {
		return fmt.Errorf("支付宝签名格式不正确: %w", err)
	}
	sum := sha256.Sum256([]byte(signContent(params)))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, sum[:], signature); err != nil {
		return fmt.Errorf("支付宝通知验签失败: %w", err)
	}
	return nil
}

// privateKey 读取并解析支付宝应用私钥。
func (c *Client) privateKey() (*rsa.PrivateKey, error) {
	key := strings.TrimSpace(c.cfg.PrivateKey)
	if key == "" && strings.TrimSpace(c.cfg.PrivateKeyPath) != "" {
		data, err := os.ReadFile(strings.TrimSpace(c.cfg.PrivateKeyPath))
		if err != nil {
			return nil, fmt.Errorf("读取支付宝应用私钥失败: %w", err)
		}
		key = strings.TrimSpace(string(data))
	}
	if key == "" {
		return nil, errors.New("支付宝应用私钥未配置")
	}
	if !strings.Contains(key, "BEGIN") {
		key = "-----BEGIN PRIVATE KEY-----\n" + key + "\n-----END PRIVATE KEY-----"
	}
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return nil, errors.New("支付宝应用私钥格式不正确")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if pkcs1Key, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes); pkcs1Err == nil {
			return pkcs1Key, nil
		}
		return nil, fmt.Errorf("解析支付宝应用私钥失败: %w", err)
	}
	privateKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("支付宝应用私钥不是 RSA 私钥")
	}
	return privateKey, nil
}

// publicKey 读取并解析支付宝公钥。
func (c *Client) publicKey() (*rsa.PublicKey, error) {
	key := strings.TrimSpace(c.cfg.PublicKey)
	if key == "" && strings.TrimSpace(c.cfg.PublicKeyPath) != "" {
		data, err := os.ReadFile(strings.TrimSpace(c.cfg.PublicKeyPath))
		if err != nil {
			return nil, fmt.Errorf("读取支付宝公钥失败: %w", err)
		}
		key = strings.TrimSpace(string(data))
	}
	if key == "" {
		return nil, errors.New("支付宝公钥未配置")
	}
	if !strings.Contains(key, "BEGIN") {
		key = "-----BEGIN PUBLIC KEY-----\n" + key + "\n-----END PUBLIC KEY-----"
	}
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return nil, errors.New("支付宝公钥格式不正确")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if pkcs1Key, pkcs1Err := x509.ParsePKCS1PublicKey(block.Bytes); pkcs1Err == nil {
			return pkcs1Key, nil
		}
		if cert, certErr := x509.ParseCertificate(block.Bytes); certErr == nil {
			if publicKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
				return publicKey, nil
			}
		}
		return nil, fmt.Errorf("解析支付宝公钥失败: %w", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("支付宝公钥不是 RSA 公钥")
	}
	return publicKey, nil
}

// signContent 按支付宝规则拼接待签名字符串。
func signContent(params url.Values) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "sign" && params.Get(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params.Get(key))
	}
	return strings.Join(parts, "&")
}

// executeFormRequest 以表单方式请求支付宝开放平台网关。
func (c *Client) executeFormRequest(ctx context.Context, params url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gatewayURL(), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, safeRequestError(err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// alipayResponseError 将支付宝接口错误统一转换为可读信息。
func alipayResponseError(code, subMsg, msg, fallback string) error {
	message := strings.TrimSpace(subMsg)
	if message == "" {
		message = strings.TrimSpace(msg)
	}
	if message == "" {
		message = fallback
	}
	if strings.TrimSpace(code) == "" {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %s", message, strings.TrimSpace(code))
}

// centsToYuan 将分转换为支付宝接口要求的元字符串。
func centsToYuan(amountCents int64) string {
	return fmt.Sprintf("%.2f", float64(amountCents)/100)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// gatewayURL 返回支付宝开放平台网关地址。
func (c *Client) gatewayURL() string {
	if strings.TrimSpace(c.cfg.GatewayURL) != "" {
		return strings.TrimSpace(c.cfg.GatewayURL)
	}
	return defaultGatewayURL
}

// safeRequestError 返回不包含请求 URL 的支付宝网关错误，避免签名参数进入日志。
func safeRequestError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("支付宝网关请求失败: %s %v", urlErr.Op, urlErr.Err)
	}
	return fmt.Errorf("支付宝网关请求失败: %v", err)
}
