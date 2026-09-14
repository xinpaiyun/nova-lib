package aiobs

import "sync"

// 货币单位。
const (
	CurrencyUSD = "USD"
	CurrencyCNY = "CNY"
)

// ModelPrice 模型单价：每 100 万 token 的价格，币种由 Currency 标注。
type ModelPrice struct {
	PromptPer1M     float64 `json:"promptPer1M"`
	CompletionPer1M float64 `json:"completionPer1M"`
	Currency        string  `json:"currency"`
}

// Cost 按 token 用量计算成本（模型币种下的金额）。
func (p ModelPrice) Cost(promptTokens, completionTokens int) float64 {
	return (float64(promptTokens)*p.PromptPer1M + float64(completionTokens)*p.CompletionPer1M) / 1_000_000
}

// 内置参考价格目录（厂商公开牌价，随调价可能过期；业务方可用 SetModelPrices 覆盖）。
var builtinPrices = map[string]ModelPrice{
	// OpenAI（USD / 1M tokens）
	"gpt-5":        {1.25, 10, CurrencyUSD},
	"gpt-5-mini":   {0.25, 2, CurrencyUSD},
	"gpt-4.1":      {2, 8, CurrencyUSD},
	"gpt-4.1-mini": {0.4, 1.6, CurrencyUSD},
	"gpt-4.1-nano": {0.1, 0.4, CurrencyUSD},
	"gpt-4o":       {2.5, 10, CurrencyUSD},
	"gpt-4o-mini":  {0.15, 0.6, CurrencyUSD},
	"o4-mini":      {1.1, 4.4, CurrencyUSD},

	// 阿里通义千问·百炼（CNY / 1M tokens）
	"qwen-turbo":    {0.3, 0.6, CurrencyCNY},
	"qwen-plus":     {0.8, 2, CurrencyCNY},
	"qwen-max":      {2.4, 9.6, CurrencyCNY},
	"qwen3-vl-plus": {1.5, 4.5, CurrencyCNY},
	"qwen-vl-plus":  {1.5, 4.5, CurrencyCNY},
	"qwen-vl-max":   {3, 9, CurrencyCNY},
	"qwen3.5-ocr":   {0.5, 1.5, CurrencyCNY},

	// DeepSeek（CNY / 1M tokens）
	"deepseek-chat":     {2, 8, CurrencyCNY},
	"deepseek-reasoner": {4, 16, CurrencyCNY},

	// 字节豆包（CNY / 1M tokens）
	"doubao-seed-1.6-flash":  {0.15, 1.5, CurrencyCNY},
	"doubao-seed-1.6":        {0.8, 8, CurrencyCNY},
	"doubao-1.5-vision-lite": {1.5, 4.5, CurrencyCNY},

	// 月之暗面 Kimi（CNY / 1M tokens）
	"moonshot-v1-8k":       {12, 12, CurrencyCNY},
	"kimi-k2-0711-preview": {4, 16, CurrencyCNY},

	// 智谱 GLM（CNY / 1M tokens）
	"glm-4-flash": {0, 0, CurrencyCNY},
	"glm-4.5":     {4, 16, CurrencyCNY},
}

var (
	priceMu       sync.RWMutex
	overridPrices map[string]ModelPrice
)

// LookupModelPrice 查询模型单价：优先业务方覆盖价，其次内置目录；未收录返回 false。
func LookupModelPrice(model string) (ModelPrice, bool) {
	priceMu.RLock()
	defer priceMu.RUnlock()
	if price, ok := overridPrices[model]; ok {
		return price, true
	}
	price, ok := builtinPrices[model]
	return price, ok
}

// SetModelPrices 设置/覆盖模型单价（如百炼自定义部署名、私有化牌价），keys 传模型名。
func SetModelPrices(prices map[string]ModelPrice) {
	priceMu.Lock()
	defer priceMu.Unlock()
	if overridPrices == nil {
		overridPrices = make(map[string]ModelPrice, len(prices))
	}
	for model, price := range prices {
		overridPrices[model] = price
	}
}

// applyPricing 为记录补齐成本字段：未显式计费的记录按价格目录计算；
// 未收录模型 Priced=false 且 Cost=0，运营侧可据此发现需要补价的模型。
func applyPricing(record *Record) {
	if record.PromptTokens <= 0 && record.CompletionTokens <= 0 {
		return
	}
	price, ok := LookupModelPrice(record.Model)
	if !ok {
		record.Priced = false
		return
	}
	record.Priced = true
	record.Currency = price.Currency
	if record.Cost == 0 {
		record.Cost = price.Cost(record.PromptTokens, record.CompletionTokens)
	}
}
