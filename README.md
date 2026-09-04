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

### 第三方能力

| 包 | 说明 | 状态 |
|---|---|---|
| `sms` | 阿里云短信验证码 | ✅ 已迁入（模板版） |
| `wechat` | 小程序登录 + 微信支付 V3 | ✅ 已迁入（模板版，业务项目分叉版待合并） |
| `storage` | 本地/S3 兼容对象存储 | ✅ 已迁入（模板版，baoxian OSS 752 行版待合并） |
| `mail` | SMTP 邮件 | ✅ 已迁入 |
| `alipay` / `tencentmap` / `shengwang` / `aliyunocr` / `openai` | 支付宝、腾讯地图、声网、OCR、LLM | ⏳ 待从 xingxueji/baoxian 抽取 |

### 平台能力

| 包 | 说明 | 状态 |
|---|---|---|
| `datacipher` | AES-GCM 字段加密、HMAC 检索哈希、脱敏 | ✅ 已迁入 |
| `queue` | Redis Streams 任务队列与 Worker | ✅ 已迁入 |
| `events` | 内存 SSE 发布订阅 Hub | ✅ 已迁入 |
| `event` | 事务性 Outbox 事件中心（重试/死信/幂等消费） | ✅ 已迁入 |
| `dataaudit` | 租户级数据操作审计 | ✅ 已迁入 |
| `tenant` / `authz` / `tenantrole` / `masking` / `middleware` / `bootstrap` | 租户上下文、RBAC、审计、脱敏、HTTP 中间件、平台初始化 | ⏳ 待迁入（middleware 依赖 foundation model，需先接口化） |

## 迁移路线图

1. ✅ **骨架 + 模板基线包迁入**（本仓库当前状态，以 nova 模板 `internal/shared` 为基线，代码带测试）。
2. ⏳ **nova 模板切换依赖**：模板 `internal/shared` 删除已迁入的包，改为 `import github.com/xinpaiyun/nova-lib/...`，新项目生成即受益。
3. ⏳ **业务项目分叉版合并**：按包评审 baoxian / xingxueji 的差异实现（优先 `wechat/pay`、`storage/oss`、`sms`），以功能超集合并进 nova-lib，打 `v1.0.0` tag。
4. ⏳ **存量项目逐个切换**：baoxian → xingxueji → lvyouji → 其余，按迭代节奏替换 import 并删除本地副本。
5. ⏳ **前端包**：`@xinpaiyun/nova-request`、`@xinpaiyun/nova-session` 等发内部 npm registry（不在本仓库）。

## 开发

```bash
make tidy   # go mod tidy
make build  # go build ./...
make test   # go test ./...
```

发版：合并到主干后打 tag（`v1.0.0`、`v1.1.0`...），各项目通过 `go get github.com/xinpaiyun/nova-lib@vX.Y.Z` 升级。
