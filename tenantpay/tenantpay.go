// Package tenantpay 提供多租户支付基础设施：
// 租户微信支付/小程序配置存储（AES-GCM 加密落库）、按租户解析支付客户端、
// 平台对租户的抽成（比例 + 单笔保底）计算与微信分账执行。
// 该包与宿主项目共用底座表 integration_configs / tenant_wechat_apps，
// 并自建 tenant_commissions / profit_sharing_orders 两张表。
package tenantpay

// 租户支付配置 provider 标识（与宿主底座约定保持一致）。
const WechatPayProvider = "integration.payment.wechat"
