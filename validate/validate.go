// Package validate 提供基于结构体 tag 的参数校验，底层使用 go-playground/validator。
package validate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// validate 基于结构体字段的 validate tag 执行参数校验。
// RegisterTagNameFunc 让错误信息中的字段名优先使用 json tag 命名。
var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(sf reflect.StructField) string {
		name := strings.SplitN(sf.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return sf.Name
		}
		return name
	})
	return v
}

// Struct 根据结构体字段的 validate tag 执行参数校验，
// 命中第一条失败规则时返回可读错误信息（如 "phone is required"）。
func Struct(v interface{}) error {
	if v == nil {
		return nil
	}
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	// 非结构体、nil 指针等非法入参保持与旧实现一致的宽松行为。
	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		return nil
	}
	var errs validator.ValidationErrors
	if errors.As(err, &errs) && len(errs) > 0 {
		return message(errs[0])
	}
	return err
}

// message 将单条校验失败翻译为面向客户端的可读错误信息。
func message(fe validator.FieldError) error {
	field := fe.Field()
	param := fe.Param()
	switch fe.Tag() {
	case "required":
		return fmt.Errorf("%s is required", field)
	case "min":
		return fmt.Errorf("%s must be at least %s", field, param)
	case "max":
		return fmt.Errorf("%s must be at most %s", field, param)
	case "gt":
		return fmt.Errorf("%s must be greater than %s", field, param)
	case "gte":
		return fmt.Errorf("%s must be greater than or equal to %s", field, param)
	case "lt":
		return fmt.Errorf("%s must be less than %s", field, param)
	case "lte":
		return fmt.Errorf("%s must be less than or equal to %s", field, param)
	case "oneof":
		return fmt.Errorf("%s must be one of %s", field, param)
	case "email":
		return fmt.Errorf("%s must be a valid email", field)
	default:
		return fmt.Errorf("%s failed %s validation", field, fe.Tag())
	}
}
