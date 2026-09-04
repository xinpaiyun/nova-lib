package password

import "testing"

// TestValidate 验证默认密码填写策略。
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "simple", value: "123456", wantErr: false},
		{name: "single", value: "a", wantErr: false},
		{name: "blank", value: "   ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
