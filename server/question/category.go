// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package question

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Category 题目类别
type Category struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IconURL   string `json:"iconUrl,omitempty"`
	GlowColor string `json:"glowColor,omitempty"`
	IsDefault bool   `json:"isDefault"`
	SortOrder int    `json:"sortOrder"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// CreateCategoryRequest 创建类别请求
type CreateCategoryRequest struct {
	Name      string `json:"name" binding:"required"`
	IconURL   string `json:"iconUrl"`
	GlowColor string `json:"glowColor"`
	SortOrder int    `json:"sortOrder"`
}

// UpdateCategoryRequest 更新类别请求
type UpdateCategoryRequest struct {
	Name      string `json:"name"`
	IconURL   string `json:"iconUrl"`
	ClearIcon bool   `json:"clearIcon"` // 是否清除图标
	GlowColor string `json:"glowColor"`
	SortOrder int    `json:"sortOrder"`
}

// HandleListCategories 获取所有题目类别
func HandleListCategories(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, name, icon_url, glow_color, is_default, sort_order, created_at, updated_at
		FROM categories
		ORDER BY id ASC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var cat Category
		var createdAt, updatedAt time.Time
		var iconURL, glowColor sql.NullString
		err := rows.Scan(&cat.ID, &cat.Name, &iconURL, &glowColor, &cat.IsDefault, &cat.SortOrder, &createdAt, &updatedAt)
		if err != nil {
			continue
		}
		if iconURL.Valid {
			cat.IconURL = iconURL.String
		}
		if glowColor.Valid {
			cat.GlowColor = glowColor.String
		}
		cat.CreatedAt = createdAt.Format(time.RFC3339)
		cat.UpdatedAt = updatedAt.Format(time.RFC3339)
		categories = append(categories, cat)
	}

	if categories == nil {
		categories = []Category{}
	}

	c.JSON(http.StatusOK, categories)
}

// HandleCreateCategory 创建题目类别
func HandleCreateCategory(c *gin.Context, db *sql.DB) {
	var req CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "details": err.Error()})
		return
	}

	// 检查名称是否已存在
	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM categories WHERE name = $1)", req.Name).Scan(&exists)
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "CATEGORY_EXISTS"})
		return
	}

	var id int64
	err := db.QueryRow(`
		INSERT INTO categories (name, icon_url, glow_color, sort_order)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4)
		RETURNING id
	`, req.Name, req.IconURL, req.GlowColor, req.SortOrder).Scan(&id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Category created"})
}

// HandleGetCategory 获取单个题目类别
func HandleGetCategory(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var cat Category
	var createdAt, updatedAt time.Time
	var iconURL, glowColor sql.NullString
	err := db.QueryRow(`
		SELECT id, name, icon_url, glow_color, is_default, sort_order, created_at, updated_at
		FROM categories WHERE id = $1
	`, id).Scan(&cat.ID, &cat.Name, &iconURL, &glowColor, &cat.IsDefault, &cat.SortOrder, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	if iconURL.Valid {
		cat.IconURL = iconURL.String
	}
	if glowColor.Valid {
		cat.GlowColor = glowColor.String
	}
	cat.CreatedAt = createdAt.Format(time.RFC3339)
	cat.UpdatedAt = updatedAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, cat)
}

// HandleUpdateCategory 更新题目类别
func HandleUpdateCategory(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为默认类别
	var isDefault bool
	err := db.QueryRow("SELECT is_default FROM categories WHERE id = $1", id).Scan(&isDefault)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}

	var req UpdateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 如果是默认类别，不允许修改名称
	if isDefault && req.Name != "" {
		// 检查名称是否变化
		var currentName string
		db.QueryRow("SELECT name FROM categories WHERE id = $1", id).Scan(&currentName)
		if req.Name != currentName {
			c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_RENAME_DEFAULT_CATEGORY"})
			return
		}
	}

	// 检查新名称是否与其他类别冲突
	if req.Name != "" {
		var exists bool
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM categories WHERE name = $1 AND id != $2)", req.Name, id).Scan(&exists)
		if exists {
			c.JSON(http.StatusConflict, gin.H{"error": "CATEGORY_NAME_EXISTS"})
			return
		}
	}

	// 处理图标更新逻辑
	var iconSQL string
	var args []interface{}
	if req.ClearIcon {
		// 清除图标
		iconSQL = `
			UPDATE categories SET
				name = COALESCE(NULLIF($1, ''), name),
				icon_url = NULL,
				glow_color = CASE WHEN $2 = '' THEN glow_color ELSE $2 END,
				sort_order = CASE WHEN $3 = 0 THEN sort_order ELSE $3 END,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $4
		`
		args = []interface{}{req.Name, req.GlowColor, req.SortOrder, id}
	} else if req.IconURL != "" {
		// 设置新图标
		iconSQL = `
			UPDATE categories SET
				name = COALESCE(NULLIF($1, ''), name),
				icon_url = $2,
				glow_color = CASE WHEN $3 = '' THEN glow_color ELSE $3 END,
				sort_order = CASE WHEN $4 = 0 THEN sort_order ELSE $4 END,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $5
		`
		args = []interface{}{req.Name, req.IconURL, req.GlowColor, req.SortOrder, id}
	} else {
		// 保持图标不变
		iconSQL = `
			UPDATE categories SET
				name = COALESCE(NULLIF($1, ''), name),
				glow_color = CASE WHEN $2 = '' THEN glow_color ELSE $2 END,
				sort_order = CASE WHEN $3 = 0 THEN sort_order ELSE $3 END,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $4
		`
		args = []interface{}{req.Name, req.GlowColor, req.SortOrder, id}
	}

	result, err := db.Exec(iconSQL, args...)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Category updated"})
}

// HandleDeleteCategory 删除题目类别
func HandleDeleteCategory(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为默认类别
	var isDefault bool
	err := db.QueryRow("SELECT is_default FROM categories WHERE id = $1", id).Scan(&isDefault)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}
	if isDefault {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_DELETE_DEFAULT_CATEGORY"})
		return
	}

	// 检查是否有题目使用此类别
	var count int
	db.QueryRow("SELECT COUNT(*) FROM question_bank WHERE category_id = $1", id).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "CATEGORY_IN_USE", "count": count})
		return
	}

	result, err := db.Exec("DELETE FROM categories WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Category deleted"})
}
