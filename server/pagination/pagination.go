// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

// Package pagination 提供列表接口通用的分页参数解析。
package pagination

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// Params 列表接口的分页参数。
// Enabled 为 false 表示请求方没有要求分页，处理函数应保持原有的全量返回行为，
// 以兼容尚未适配分页的前端页面。
type Params struct {
	Page     int
	PageSize int
	Offset   int
	Enabled  bool
}

// Parse 解析 page / pageSize 查询参数。
// 只有显式传入 page 或 pageSize 时才启用分页；
// page 非法时回退到第 1 页，pageSize 超出 [1, maxSize] 时回退到 defaultSize。
func Parse(c *gin.Context, defaultSize, maxSize int) Params {
	pageRaw := c.Query("page")
	sizeRaw := c.Query("pageSize")
	if pageRaw == "" && sizeRaw == "" {
		return Params{}
	}

	page, err := strconv.Atoi(pageRaw)
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.Atoi(sizeRaw)
	if err != nil || pageSize < 1 || pageSize > maxSize {
		pageSize = defaultSize
	}

	return Params{
		Page:     page,
		PageSize: pageSize,
		Offset:   (page - 1) * pageSize,
		Enabled:  true,
	}
}

// TotalPages 根据总记录数计算总页数（向上取整）。
func (p Params) TotalPages(total int) int {
	if !p.Enabled || p.PageSize <= 0 || total <= 0 {
		return 0
	}
	return (total + p.PageSize - 1) / p.PageSize
}
