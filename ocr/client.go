// Package ocr 提供阿里云文字识别（OCR）的标准能力封装：
// 通用文字、身份证、行驶证识别，支持图片 URL 与二进制流输入。
package ocr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ocrapi "github.com/alibabacloud-go/ocr-api-20210707/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/tidwall/gjson"

	"github.com/xinpaiyun/nova-lib/config"
)

// VehicleLicenseResult 定义行驶证 OCR 识别后的标准化字段。
type VehicleLicenseResult struct {
	PlateNumber  string         `json:"plateNumber"`
	OwnerName    string         `json:"ownerName"`
	VehicleType  string         `json:"vehicleType"`
	ModelName    string         `json:"modelName"`
	VIN          string         `json:"vin"`
	EngineNo     string         `json:"engineNo"`
	RegisterDate string         `json:"registerDate"`
	IssueDate    string         `json:"issueDate"`
	UseCharacter string         `json:"useCharacter"`
	Address      string         `json:"address"`
	RequestID    string         `json:"requestId"`
	Raw          map[string]any `json:"raw,omitempty"`
}

// IDCardResult 定义身份证 OCR 识别后的标准化字段。
type IDCardResult struct {
	Name           string         `json:"name"`
	Gender         string         `json:"gender"`
	Nation         string         `json:"nation"`
	BirthDate      string         `json:"birthDate"`
	Address        string         `json:"address"`
	IDNumber       string         `json:"idNumber"`
	IssueAuthority string         `json:"issueAuthority"`
	ValidPeriod    string         `json:"validPeriod"`
	ValidStartDate string         `json:"validStartDate"`
	ValidEndDate   string         `json:"validEndDate"`
	RequestID      string         `json:"requestId"`
	Raw            map[string]any `json:"raw,omitempty"`
}

// GeneralTextResult 定义通用文字识别结果。
type GeneralTextResult struct {
	Text      string         `json:"text"`
	RequestID string         `json:"requestId"`
	Raw       map[string]any `json:"raw,omitempty"`
}

// Client 封装阿里云 OCR 调用能力。
type Client struct {
	cfg    config.OCRConfig
	client *ocrapi.Client
}

// NewClient 创建阿里云 OCR 客户端。
func NewClient(cfg config.OCRConfig) (*Client, error) {
	if !cfg.Enabled {
		return &Client{cfg: cfg}, nil
	}
	if strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.AccessKeySecret) == "" {
		return nil, errors.New("aliyun ocr access key is required")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = "ocr-api.cn-hangzhou.aliyuncs.com"
	}
	ocrClient, err := ocrapi.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(strings.TrimSpace(cfg.AccessKeyID)),
		AccessKeySecret: tea.String(strings.TrimSpace(cfg.AccessKeySecret)),
		Endpoint:        tea.String(endpoint),
	})
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, client: ocrClient}, nil
}

// IsEnabled 判断 OCR 是否可用。
func (c *Client) IsEnabled() bool {
	return c != nil && c.cfg.Enabled && c.client != nil
}

// RecognizeGeneralByReader 通过图片或文档二进制流执行通用文字识别。
func (c *Client) RecognizeGeneralByReader(ctx context.Context, body io.Reader) (*GeneralTextResult, error) {
	if !c.IsEnabled() {
		return nil, errors.New("阿里云 OCR 未启用")
	}
	if body == nil {
		return nil, errors.New("识别文件不能为空")
	}
	resp, err := c.client.RecognizeGeneral(&ocrapi.RecognizeGeneralRequest{
		Body: body,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("OCR 响应为空")
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "" && code != "200" {
		msg := tea.StringValue(resp.Body.Message)
		if msg == "" {
			msg = "通用文字识别失败"
		}
		return nil, errors.New(msg)
	}
	return parseGeneralTextData(tea.StringValue(resp.Body.RequestId), tea.StringValue(resp.Body.Data)), nil
}

// RecognizeIDCardByReader 通过图片二进制流识别身份证。
func (c *Client) RecognizeIDCardByReader(ctx context.Context, body io.Reader) (*IDCardResult, error) {
	if !c.IsEnabled() {
		return nil, errors.New("阿里云 OCR 未启用")
	}
	if body == nil {
		return nil, errors.New("身份证图片不能为空")
	}
	resp, err := c.client.RecognizeIdcard(&ocrapi.RecognizeIdcardRequest{
		Body: body,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("OCR 响应为空")
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "" && code != "200" {
		msg := tea.StringValue(resp.Body.Message)
		if msg == "" {
			msg = "身份证识别失败"
		}
		return nil, errors.New(msg)
	}
	return parseIDCardData(tea.StringValue(resp.Body.RequestId), tea.StringValue(resp.Body.Data)), nil
}

// RecognizeVehicleLicenseByURL 通过图片 URL 识别行驶证。
func (c *Client) RecognizeVehicleLicenseByURL(ctx context.Context, imageURL string) (*VehicleLicenseResult, error) {
	if !c.IsEnabled() {
		return nil, errors.New("阿里云 OCR 未启用")
	}
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, errors.New("行驶证图片地址不能为空")
	}
	resp, err := c.client.RecognizeVehicleLicense(&ocrapi.RecognizeVehicleLicenseRequest{
		Url: tea.String(imageURL),
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("OCR 响应为空")
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "" && code != "200" {
		msg := tea.StringValue(resp.Body.Message)
		if msg == "" {
			msg = "行驶证识别失败"
		}
		return nil, errors.New(msg)
	}
	return parseVehicleLicenseData(tea.StringValue(resp.Body.RequestId), tea.StringValue(resp.Body.Data)), nil
}

// RecognizeVehicleLicenseByReader 通过图片二进制流识别行驶证。
func (c *Client) RecognizeVehicleLicenseByReader(ctx context.Context, body io.Reader) (*VehicleLicenseResult, error) {
	if !c.IsEnabled() {
		return nil, errors.New("阿里云 OCR 未启用")
	}
	if body == nil {
		return nil, errors.New("行驶证图片不能为空")
	}
	resp, err := c.client.RecognizeVehicleLicense(&ocrapi.RecognizeVehicleLicenseRequest{
		Body: body,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("OCR 响应为空")
	}
	code := tea.StringValue(resp.Body.Code)
	if code != "" && code != "200" {
		msg := tea.StringValue(resp.Body.Message)
		if msg == "" {
			msg = "行驶证识别失败"
		}
		return nil, errors.New(msg)
	}
	return parseVehicleLicenseData(tea.StringValue(resp.Body.RequestId), tea.StringValue(resp.Body.Data)), nil
}

// parseGeneralTextData 解析阿里云通用文字识别 Data 字段。
func parseGeneralTextData(requestID string, data string) *GeneralTextResult {
	raw := map[string]any{}
	_ = json.Unmarshal([]byte(data), &raw)
	texts := make([]string, 0)
	collectGeneralText(raw, &texts)
	text := strings.Join(texts, "\n")
	if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(data)
	}
	if strings.TrimSpace(text) == "" {
		slog.Warn("aliyun general ocr returned no text", "aliyun_request_id", requestID)
	}
	return &GeneralTextResult{Text: text, RequestID: requestID, Raw: raw}
}

// collectGeneralText 从通用 OCR JSON 中递归收集文本字段。
func collectGeneralText(value any, texts *[]string) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			lower := strings.ToLower(key)
			if lower == "text" || lower == "content" || lower == "word" || lower == "value" {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					*texts = append(*texts, strings.TrimSpace(text))
					continue
				}
			}
			collectGeneralText(child, texts)
		}
	case []any:
		for _, child := range item {
			collectGeneralText(child, texts)
		}
	case string:
		if strings.TrimSpace(item) != "" && len([]rune(item)) > 1 {
			*texts = append(*texts, strings.TrimSpace(item))
		}
	}
}

// parseIDCardData 解析阿里云 OCR 身份证 Data 字段。
func parseIDCardData(requestID string, data string) *IDCardResult {
	raw := map[string]any{}
	_ = json.Unmarshal([]byte(data), &raw)
	result := &IDCardResult{
		Name: firstJSONValue(data,
			"name", "Name", "姓名", "face.name", "face.Name", "data.name", "data.Name", "data.face.name", "data.face.Name", "data.face.data.name",
		),
		Gender: firstJSONValue(data,
			"sex", "gender", "Sex", "Gender", "性别", "face.sex", "face.gender", "data.sex", "data.gender", "data.face.sex", "data.face.gender", "data.face.data.sex",
		),
		Nation: firstJSONValue(data,
			"nationality", "nation", "Nationality", "Nation", "民族", "face.nationality", "face.nation", "data.nationality", "data.nation", "data.face.nation", "data.face.data.nation",
		),
		BirthDate: firstJSONValue(data,
			"birthDate", "birth", "birthday", "dateOfBirth", "出生", "face.birthDate", "face.birth", "data.birthDate", "data.birth", "data.face.birthDate", "data.face.data.birthDate",
		),
		Address: firstJSONValue(data,
			"address", "Address", "住址", "face.address", "data.address", "data.face.address", "data.face.data.address",
		),
		IDNumber: firstJSONValue(data,
			"idNumber", "idNo", "idcardNumber", "cardNumber", "number", "身份证号", "公民身份号码",
			"face.idNumber", "face.idNo", "data.idNumber", "data.idNo", "data.face.idNumber", "data.face.data.idNumber",
		),
		IssueAuthority: firstJSONValue(data,
			"issueAuthority", "authority", "issue", "签发机关", "back.issueAuthority", "back.authority", "data.issueAuthority", "data.authority", "data.back.issueAuthority", "data.back.data.issueAuthority",
		),
		ValidPeriod: firstJSONValue(data,
			"validPeriod", "valid_date", "validDate", "有效期限", "back.validPeriod", "back.validDate", "data.validPeriod", "data.validDate", "data.back.validPeriod", "data.back.data.validPeriod",
		),
		ValidStartDate: firstJSONValue(data,
			"validStartDate", "startDate", "validFrom", "back.validStartDate", "back.startDate", "data.validStartDate", "data.startDate", "data.back.validStartDate",
		),
		ValidEndDate: firstJSONValue(data,
			"validEndDate", "endDate", "validTo", "back.validEndDate", "back.endDate", "data.validEndDate", "data.endDate", "data.back.validEndDate",
		),
		RequestID: requestID,
		Raw:       raw,
	}
	if strings.TrimSpace(result.IDNumber) == "" && strings.TrimSpace(result.Name) == "" && strings.TrimSpace(result.IssueAuthority) == "" {
		slog.Warn("aliyun id card ocr returned no primary fields", "aliyun_request_id", requestID)
	}
	return result
}

// parseVehicleLicenseData 解析阿里云 OCR 行驶证 Data 字段。
func parseVehicleLicenseData(requestID string, data string) *VehicleLicenseResult {
	raw := map[string]any{}
	_ = json.Unmarshal([]byte(data), &raw)
	result := &VehicleLicenseResult{
		PlateNumber: firstJSONValue(data,
			"plateNumber", "plate_no", "plate_number", "PlateNumber", "plateNum", "licensePlateNumber",
			"face.plateNumber", "face.plate_no", "face.plate_number",
			"faceResult.plateNumber", "data.plateNumber", "data.plate_number", "data.face.plateNumber", "data.face.plate_number", "data.face.data.licensePlateNumber", "data.face.data.plateNumber", "vehicle.plateNumber",
		),
		OwnerName: firstJSONValue(data,
			"owner", "ownerName", "owner_name", "Owner",
			"face.owner", "face.ownerName", "face.owner_name",
			"faceResult.owner", "data.owner", "data.ownerName", "data.owner_name", "data.face.owner", "data.face.ownerName", "data.face.owner_name", "data.face.data.owner", "data.face.data.ownerName", "vehicle.ownerName",
		),
		VehicleType: firstJSONValue(data,
			"vehicleType", "vehicle_type", "VehicleType",
			"face.vehicleType", "face.vehicle_type", "faceResult.vehicleType",
			"data.vehicleType", "data.vehicle_type", "data.face.vehicleType", "data.face.vehicle_type", "data.face.data.vehicleType", "data.face.data.vehicle_type", "vehicle.vehicleType",
		),
		ModelName: firstJSONValue(data,
			"model", "brandModel", "brand_model", "modelName", "model_name",
			"face.model", "face.brandModel", "face.brand_model", "faceResult.model",
			"data.model", "data.brandModel", "data.brand_model", "data.face.model", "data.face.brandModel", "data.face.brand_model", "data.face.data.model", "data.face.data.brandModel", "vehicle.modelName",
		),
		VIN: firstJSONValue(data,
			"vin", "VIN", "vinCode", "vehicleIdentificationNumber", "vehicle_identification_number",
			"face.vin", "face.VIN", "face.vehicleIdentificationNumber", "faceResult.vin",
			"data.vin", "data.VIN", "data.vinCode", "data.vehicleIdentificationNumber", "data.vehicle_identification_number", "data.face.vin", "data.face.VIN", "data.face.vehicleIdentificationNumber", "data.face.data.vinCode", "data.face.data.vin", "vehicle.vin",
		),
		EngineNo: firstJSONValue(data,
			"engineNo", "engineNumber", "engine_no", "engine_number", "EngineNo",
			"face.engineNo", "face.engineNumber", "face.engine_no", "faceResult.engineNo",
			"data.engineNo", "data.engineNumber", "data.engine_no", "data.engine_number", "data.face.engineNo", "data.face.engineNumber", "data.face.engine_number", "data.face.data.engineNumber", "data.face.data.engineNo", "vehicle.engineNo",
		),
		RegisterDate: firstJSONValue(data,
			"registerDate", "registrationDate", "register_date", "registration_date",
			"face.registerDate", "face.registrationDate", "faceResult.registerDate",
			"data.registerDate", "data.registrationDate", "data.register_date", "data.registration_date", "data.face.registerDate", "data.face.registrationDate", "data.face.register_date", "data.face.data.registrationDate", "data.face.data.registerDate", "vehicle.registerDate",
		),
		IssueDate: firstJSONValue(data,
			"issueDate", "issue_date", "face.issueDate", "face.issue_date",
			"faceResult.issueDate", "data.issueDate", "data.issue_date", "data.face.issueDate", "data.face.issue_date", "data.face.data.issueDate", "vehicle.issueDate",
		),
		UseCharacter: firstJSONValue(data,
			"useCharacter", "useNature", "use_character", "use_nature",
			"face.useCharacter", "face.useNature", "faceResult.useCharacter",
			"data.useCharacter", "data.useNature", "data.use_character", "data.use_nature", "data.face.useCharacter", "data.face.useNature", "data.face.use_character", "data.face.data.useNature", "data.face.data.useCharacter", "vehicle.useCharacter",
		),
		Address: firstJSONValue(data,
			"address", "licenseAddress", "license_address",
			"face.address", "faceResult.address", "data.address", "data.licenseAddress", "data.license_address", "data.face.address", "data.face.data.address", "vehicle.licenseAddress",
		),
		RequestID: requestID,
		Raw:       raw,
	}
	if !hasVehicleLicensePrimaryFields(result) {
		slog.Warn("aliyun vehicle license ocr returned no primary fields", "aliyun_request_id", requestID)
	}
	return result
}

// hasVehicleLicensePrimaryFields 判断 OCR 是否解析出可直接回填车辆表单的关键字段。
func hasVehicleLicensePrimaryFields(result *VehicleLicenseResult) bool {
	if result == nil {
		return false
	}
	return strings.TrimSpace(result.PlateNumber) != "" ||
		strings.TrimSpace(result.VIN) != "" ||
		strings.TrimSpace(result.EngineNo) != "" ||
		strings.TrimSpace(result.ModelName) != ""
}

// firstJSONValue 从多个可能路径中取第一个非空字符串。
func firstJSONValue(raw string, paths ...string) string {
	for _, path := range paths {
		value := strings.TrimSpace(gjson.Get(raw, path).String())
		if value != "" {
			return value
		}
	}
	return ""
}
