# nova-lib

公司级 Go 基础能力库。各业务项目（baoxian、xingxueji、lvyouji 等）与 nova 脚手架共享登录、支付、短信、OSS、多租户等基础能力，一处修改、按版本升级。

- Module: `github.com/xinpaiyun/nova-lib`
- Go: 1.25+
- 配置约定：各包接收 `nova-lib/config` 中的结构体，由项目自身的 `config.yaml` / `config.local.yaml` 加载后注入，库内不读环境变量（datacipher 的密钥覆盖除外）。

## 包清单

### 基础设施

| 包 | 说明 | 状态 |
|---|---|---|
| `config` | 共享配置结构体（database/redis/jwt/sms/wechat/storage/mail/ocr/ai/bootstrap 等） | ✅ 已迁入 |
| `apperror` | 统一业务错误类型与稳定错误码 | ✅ 已迁入 |
| `response` | Hertz 统一 API 响应封装 | ✅ 已迁入 |
| `pagination` | 统一分页参数与结果 | ✅ 已迁入 |
| `database` | GORM 初始化、连接池、事务上下文传递 | ✅ 已迁入 |
| `redis` | Redis 客户端管理 | ✅ 已迁入 |
| `cache` | Redis 缓存，未启用时回退内存缓存 | ✅ 已迁入 |
| `auth` | JWT 签发与解析 | ✅ 已迁入 |
| `validate` | 结构体 tag 参数校验 | ✅ 已迁入 |
| `password` | 密码基础校验 | ✅ 已迁入 |
| `logging` | logrus JSON 结构化日志 + hlog 适配 + 请求追踪 ID + GORM 日志适配器 | ✅ v1.3.0 |
| `metrics` | 进程内 HTTP 请求指标记录与快照 | ✅ v1.3.0 |
| `middleware` | Hertz 通用中间件：RequestID/CORS/安全头/Recovery/访问日志/限流/租户解析/JWT 鉴权（会话校验与日志落库通过回调注入） | ✅ v1.3.0 |

### 第三方能力

| 包 | 说明 | 状态 |
|---|---|---|
| `sms` | 阿里云短信验证码 | ✅ v1.0.0 |
| `wechat` | 小程序登录 + 微信支付 V3 | ✅ v1.0.0 |
| `storage` | 本地/S3 兼容对象存储（含临时 URL、对象移动） | ✅ v1.0.0 |
| `mail` | SMTP 邮件 | ✅ 已迁入 |
| `alipay` | 支付宝开放平台：OAuth 登录、手机号解密、JSAPI 交易创建/查询、通知验签（RSA2 + AES-CBC，纯标准库） | ✅ v1.1.0 |
| `tencentmap` | 腾讯位置服务：逆地理编码（sig 签名 + Redis 缓存） | ✅ v1.1.0 |
| `ocr` | 阿里云 OCR：通用文字 / 身份证 / 行驶证识别（URL 与二进制流） | ✅ v1.1.0 |
| `openai` | OpenAI 兼容协议：文本与视觉对话（含 token 用量统计） | ✅ v1.1.0 |
| `shengwang` | 声网：RTC/灵动课堂 token 签发、实时转写任务 | ✅ v1.1.0 |

### 平台能力

| 包 | 说明 | 状态 |
|---|---|---|
| `datacipher` | AES-GCM 字段加密、HMAC 检索哈希、脱敏 | ✅ 已迁入 |
| `queue` | Redis Streams 任务队列与 Worker | ✅ 已迁入 |
| `events` | 内存 SSE 发布订阅 Hub | ✅ 已迁入 |
| `event` | 事务性 Outbox 事件中心（重试/死信/幂等消费） | ✅ 已迁入 |
| `dataaudit` | 租户级数据操作审计 | ✅ 已迁入 |
| `tenant` / `authz` / `tenantrole` / `masking` / `bootstrap` | 租户上下文、RBAC、审计、脱敏、平台初始化 | ⏳ 待迁入（依赖各项目 model，需先接口化） |

## 迁移路线图

1. ✅ **骨架 + 模板基线包迁入**（本仓库当前状态，以 nova 模板 `internal/shared` 为基线，代码带测试）。
2. ✅ **nova 模板切换依赖**（v0.1.0）：模板 `internal/shared` 已删除迁入的 17 个包并改为 `import github.com/xinpaiyun/nova-lib/...`，config 基础结构体改为 nova-lib 类型别名，新项目生成即受益。
3. ✅ **业务项目分叉版合并**（v1.0.0）：
   - `storage`：Store 接口新增 `PresignGetURL` / `MoveFile`（源自 xingxueji 私有桶与临时 URL 能力），S3 客户端启用 `RequestChecksumCalculationWhenRequired`（阿里云 OSS S3 兼容必需，源自 baoxian 踩坑）
   - `wechat`：Client 升级为标准微信 API 超集——`Session`（完整 code2session 响应）、`FetchPhoneNumber`（自动 access_token 换取手机号）、`AccessToken`（Redis 缓存 + 临期刷新）、`GenerateUnlimitedQRCode`（小程序码）、`SendSubscribeMessage`（订阅消息）；`Code2Session` 旧签名保留兼容。Pay 维持 wechatpay-go 标准 V3 实现（双端 appid 等业务定制不进库）
   - `sms`：模板版已是超集（返回验证码、原子 GetDel、租户级配置），业务版无需合并
4. ✅ **缺失能力补全**（v1.1.0）：从业务项目抽取 `alipay`（单应用标准版，去除双端 appType）、`tencentmap`、`ocr`（baoxian 超集版）、`openai`（含 token 用量）、`shengwang`（含官方 token 算法）；`cache` 新增 `SetJSON`/`GetJSON`；`config` 新增 `AlipayConfig`/`TencentMapConfig`/`ShengwangConfig`，`AIConfig` 补 `Model`/`TimeoutSec`
5. ✅ **通用中间件三件套迁入**（v1.3.0）：`logging`（logrus JSON + hlog 适配 + GORM 适配器）、`metrics`（进程内 HTTP 指标）、`middleware`（RequestID/CORS/安全头/Recovery/访问日志/限流/租户解析/RequireAuth）；业务侧会话校验（validateSession）与访问日志落库通过 `SessionValidator`/`AccessLogRecorder` 回调注入，`config` 新增 `SecurityHeadersConfig`
6. ⏳ **存量项目逐个切换**：✅ chongyuji（response）→ lvyouji → chehuixing → xingxueji → baoxian，按迭代节奏替换 import 并删除本地副本。
7. ⏳ **前端包**：`@xinpaiyun/nova-request`、`@xinpaiyun/nova-session` 等发内部 npm registry（不在本仓库）。

## 开发

```bash
make tidy   # go mod tidy
make build  # go build ./...
make test   # go test ./...
```

发版：合并到主干后打 tag（`v1.0.0`、`v1.1.0`...），各项目通过 `go get github.com/xinpaiyun/nova-lib@vX.Y.Z` 升级。
