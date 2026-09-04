package auth

import "context"

// contextKeyClaims 是认证声明在上下文中的键。
type contextKeyClaims struct{}

// ContextWithClaims 将认证声明写入请求上下文。
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	if claims == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKeyClaims{}, claims)
}

// ClaimsFromContext 从请求上下文读取认证声明。
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(contextKeyClaims{}).(*Claims)
	return claims, ok && claims != nil
}

// UserIDFromContext 从请求上下文读取当前用户 ID。
func UserIDFromContext(ctx context.Context) (uint64, bool) {
	claims, ok := ClaimsFromContext(ctx)
	if !ok || claims.UserID == 0 {
		return 0, false
	}
	return claims.UserID, true
}
