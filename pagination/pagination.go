// Package pagination 提供统一的分页参数与结果结构。
package pagination

// Query 表示统一分页查询参数。
type Query struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// Result 表示统一分页响应结构。
type Result[T any] struct {
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int64 `json:"total"`
	Items    []T   `json:"items"`
}

// Normalize 规范化分页参数并限制最大页容量。
func Normalize(page int, pageSize int) Query {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return Query{Page: page, PageSize: pageSize}
}

// Offset 返回数据库分页偏移量。
func (q Query) Offset() int {
	return (q.Page - 1) * q.PageSize
}
