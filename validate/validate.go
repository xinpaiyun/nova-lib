// Package validate 提供基于结构体 tag 的基础参数校验。
package validate

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Struct 根据结构体字段的 validate tag 执行基础参数校验。
func Struct(v interface{}) error {
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.Ptr {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil
	}

	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := typ.Field(i)
		tag := fieldType.Tag.Get("validate")
		if tag == "" || tag == "-" {
			continue
		}
		name := fieldName(fieldType)
		for _, rule := range strings.Split(tag, ",") {
			if err := validateRule(field, name, rule); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateRule 执行单条校验规则。
func validateRule(field reflect.Value, name, rule string) error {
	switch {
	case rule == "required":
		if isZero(field) {
			return fmt.Errorf("%s is required", name)
		}
	case strings.HasPrefix(rule, "min="):
		min, err := strconv.ParseFloat(strings.TrimPrefix(rule, "min="), 64)
		if err != nil {
			return nil
		}
		if !matchMin(field, min) {
			return fmt.Errorf("%s must be at least %s", name, strings.TrimPrefix(rule, "min="))
		}
	}
	return nil
}

// fieldName 返回用于错误提示的 JSON 字段名。
func fieldName(fieldType reflect.StructField) string {
	jsonTag := fieldType.Tag.Get("json")
	if jsonTag == "" || jsonTag == "-" {
		return fieldType.Name
	}
	return strings.Split(jsonTag, ",")[0]
}

// isZero 判断字段是否为空值。
func isZero(field reflect.Value) bool {
	if field.Kind() == reflect.Ptr {
		return field.IsNil()
	}
	return field.IsZero()
}

// matchMin 判断字段是否满足 min 规则。
func matchMin(field reflect.Value, min float64) bool {
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return true
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return float64(field.Len()) >= min
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(field.Int()) >= min
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(field.Uint()) >= min
	case reflect.Float32, reflect.Float64:
		return field.Float() >= min
	default:
		return true
	}
}
