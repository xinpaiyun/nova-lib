package tenant

import (
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

// TestTenantIDAndUserID 验证上下文读取与缺省值。
func TestTenantIDAndUserID(t *testing.T) {
	ctx := app.NewContext(1)
	ctx.Set(TenantIDKey, uint64(7))
	ctx.Set(UserIDKey, uint64(9))

	if got := TenantID(ctx); got != 7 {
		t.Fatalf("TenantID() = %d, want 7", got)
	}
	if got := UserID(ctx); got != 9 {
		t.Fatalf("UserID() = %d, want 9", got)
	}

	empty := app.NewContext(1)
	if got := TenantID(empty); got != 0 {
		t.Fatalf("missing TenantID() = %d, want 0", got)
	}
	if got := UserID(empty); got != 0 {
		t.Fatalf("missing UserID() = %d, want 0", got)
	}
}

// TestGetUint64 验证通用 uint64 业务字段读取（如门店/团队范围）。
func TestGetUint64(t *testing.T) {
	ctx := app.NewContext(1)
	ctx.Set("member_shop_id", uint64(12))
	if got := GetUint64(ctx, "member_shop_id"); got != 12 {
		t.Fatalf("GetUint64() = %d, want 12", got)
	}
	if got := GetUint64(ctx, "not_set"); got != 0 {
		t.Fatalf("GetUint64(missing) = %d, want 0", got)
	}
}
