// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AWDFQuestion AWD-F题库题目
type AWDFQuestion struct {
	ID              int64   `json:"id"`
	Title           string  `json:"title"`
	CategoryID      int64   `json:"categoryId"`
	CategoryName    *string `json:"categoryName,omitempty"`
	Difficulty      int     `json:"difficulty"`
	Description     *string `json:"description"`
	DockerImage     string  `json:"dockerImage"`
	Ports           *string `json:"ports"`
	CPULimit        *string `json:"cpuLimit"`
	MemoryLimit     *string `json:"memoryLimit"`
	StorageLimit    *string `json:"storageLimit"`
	NoResourceLimit bool    `json:"noResourceLimit"`
	ExpScript       *string `json:"expScript"`
	CheckScript     *string `json:"checkScript"`
	PatchWhitelist  *string `json:"patchWhitelist"`
	VulnerableFile  *string `json:"vulnerableFile"`
	FlagEnv         *string `json:"flagEnv"`
	FlagScript      *string `json:"flagScript"`
	ImageStatus     *string `json:"imageStatus"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

// nullStringToPtr 将 sql.NullString 转换为 *string
func nullStringToPtr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

// NullIfEmpty 如果字符串为空返回nil
func NullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// CreateAWDFQuestionRequest 创建AWD-F题目请求
type CreateAWDFQuestionRequest struct {
	Title           string `json:"title" binding:"required"`
	CategoryID      int64  `json:"categoryId" binding:"required"`
	Difficulty      int    `json:"difficulty"`
	Description     string `json:"description"`
	DockerImage     string `json:"dockerImage" binding:"required"`
	Ports           string `json:"ports"`
	CPULimit        string `json:"cpuLimit"`
	MemoryLimit     string `json:"memoryLimit"`
	StorageLimit    string `json:"storageLimit"`
	NoResourceLimit bool   `json:"noResourceLimit"`
	ExpScript       string `json:"expScript"`
	CheckScript     string `json:"checkScript"`
	PatchWhitelist  string `json:"patchWhitelist"`
	VulnerableFile  string `json:"vulnerableFile"`
	FlagEnv         string `json:"flagEnv"`
	FlagScript      string `json:"flagScript"`
}

// UpdateAWDFQuestionRequest 更新AWD-F题目请求
type UpdateAWDFQuestionRequest struct {
	Title           string `json:"title"`
	CategoryID      int64  `json:"categoryId"`
	Difficulty      int    `json:"difficulty"`
	Description     string `json:"description"`
	DockerImage     string `json:"dockerImage"`
	Ports           string `json:"ports"`
	CPULimit        string `json:"cpuLimit"`
	MemoryLimit     string `json:"memoryLimit"`
	StorageLimit    string `json:"storageLimit"`
	NoResourceLimit bool   `json:"noResourceLimit"`
	ExpScript       string `json:"expScript"`
	CheckScript     string `json:"checkScript"`
	PatchWhitelist  string `json:"patchWhitelist"`
	VulnerableFile  string `json:"vulnerableFile"`
	FlagEnv         string `json:"flagEnv"`
	FlagScript      string `json:"flagScript"`
}

// HandleListAWDFQuestions 获取AWD-F题库列表
func HandleListAWDFQuestions(c *gin.Context, db *sql.DB) {
	categoryID := c.Query("categoryId")
	difficulty := c.Query("difficulty")

	query := `
		SELECT q.id, q.title, q.category_id, c.name as category_name,
			q.difficulty, q.description, q.docker_image, q.ports,
			q.cpu_limit, q.memory_limit, q.storage_limit, q.no_resource_limit,
			q.exp_script, q.check_script, q.patch_whitelist, q.vulnerable_file,
			q.flag_env, q.flag_script, q.image_status,
			q.created_at, q.updated_at
		FROM question_bank_awdf q
		LEFT JOIN categories c ON q.category_id = c.id
		WHERE 1=1
	`
	args := []interface{}{}
	argIndex := 1

	if categoryID != "" {
		query += " AND q.category_id = $" + string(rune('0'+argIndex))
		args = append(args, categoryID)
		argIndex++
	}
	if difficulty != "" {
		query += " AND q.difficulty = $" + string(rune('0'+argIndex))
		args = append(args, difficulty)
		argIndex++
	}

	query += " ORDER BY q.created_at ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}
	defer rows.Close()

	var questions []AWDFQuestion
	for rows.Next() {
		var q AWDFQuestion
		var createdAt, updatedAt time.Time
		var categoryName, description, ports sql.NullString
		var cpuLimit, memoryLimit, storageLimit sql.NullString
		var expScript, checkScript, patchWhitelist, vulnerableFile sql.NullString
		var flagEnv, flagScript, imageStatus sql.NullString

		err := rows.Scan(
			&q.ID, &q.Title, &q.CategoryID, &categoryName,
			&q.Difficulty, &description, &q.DockerImage, &ports,
			&cpuLimit, &memoryLimit, &storageLimit, &q.NoResourceLimit,
			&expScript, &checkScript, &patchWhitelist, &vulnerableFile,
			&flagEnv, &flagScript, &imageStatus,
			&createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}
		q.CategoryName = nullStringToPtr(categoryName)
		q.Description = nullStringToPtr(description)
		q.Ports = nullStringToPtr(ports)
		q.CPULimit = nullStringToPtr(cpuLimit)
		q.MemoryLimit = nullStringToPtr(memoryLimit)
		q.StorageLimit = nullStringToPtr(storageLimit)
		q.ExpScript = nullStringToPtr(expScript)
		q.CheckScript = nullStringToPtr(checkScript)
		q.PatchWhitelist = nullStringToPtr(patchWhitelist)
		q.VulnerableFile = nullStringToPtr(vulnerableFile)
		q.FlagEnv = nullStringToPtr(flagEnv)
		q.FlagScript = nullStringToPtr(flagScript)
		q.ImageStatus = nullStringToPtr(imageStatus)
		q.CreatedAt = createdAt.Format(time.RFC3339)
		q.UpdatedAt = updatedAt.Format(time.RFC3339)
		questions = append(questions, q)
	}

	if questions == nil {
		questions = []AWDFQuestion{}
	}

	c.JSON(http.StatusOK, questions)
}

// HandleCreateAWDFQuestion 创建AWD-F题库题目
func HandleCreateAWDFQuestion(c *gin.Context, db *sql.DB) {
	var req CreateAWDFQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "details": err.Error()})
		return
	}

	// 验证分类是否存在
	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM categories WHERE id = $1)", req.CategoryID).Scan(&exists)
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}

	if req.Difficulty == 0 {
		req.Difficulty = 5
	}

	var id int64
	err := db.QueryRow(`
		INSERT INTO question_bank_awdf (
			title, category_id, difficulty, description, docker_image,
			ports, cpu_limit, memory_limit, storage_limit, no_resource_limit,
			exp_script, check_script, patch_whitelist, vulnerable_file,
			flag_env, flag_script
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id
	`,
		req.Title, req.CategoryID, req.Difficulty, req.Description, req.DockerImage,
		NullIfEmpty(req.Ports), NullIfEmpty(req.CPULimit),
		NullIfEmpty(req.MemoryLimit), NullIfEmpty(req.StorageLimit), req.NoResourceLimit,
		NullIfEmpty(req.ExpScript), NullIfEmpty(req.CheckScript),
		NullIfEmpty(req.PatchWhitelist), NullIfEmpty(req.VulnerableFile),
		NullIfEmpty(req.FlagEnv), NullIfEmpty(req.FlagScript),
	).Scan(&id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "AWD-F question created"})
}

// HandleGetAWDFQuestion 获取单个AWD-F题库题目
func HandleGetAWDFQuestion(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var q AWDFQuestion
	var createdAt, updatedAt time.Time
	var categoryName, description, ports sql.NullString
	var cpuLimit, memoryLimit, storageLimit sql.NullString
	var expScript, checkScript, patchWhitelist, vulnerableFile sql.NullString
	var flagEnv, flagScript, imageStatus sql.NullString

	err := db.QueryRow(`
		SELECT q.id, q.title, q.category_id, c.name as category_name,
			q.difficulty, q.description, q.docker_image, q.ports,
			q.cpu_limit, q.memory_limit, q.storage_limit, q.no_resource_limit,
			q.exp_script, q.check_script, q.patch_whitelist, q.vulnerable_file,
			q.flag_env, q.flag_script, q.image_status,
			q.created_at, q.updated_at
		FROM question_bank_awdf q
		LEFT JOIN categories c ON q.category_id = c.id
		WHERE q.id = $1
	`, id).Scan(
		&q.ID, &q.Title, &q.CategoryID, &categoryName,
		&q.Difficulty, &description, &q.DockerImage, &ports,
		&cpuLimit, &memoryLimit, &storageLimit, &q.NoResourceLimit,
		&expScript, &checkScript, &patchWhitelist, &vulnerableFile,
		&flagEnv, &flagScript, &imageStatus,
		&createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	q.CategoryName = nullStringToPtr(categoryName)
	q.Description = nullStringToPtr(description)
	q.Ports = nullStringToPtr(ports)
	q.CPULimit = nullStringToPtr(cpuLimit)
	q.MemoryLimit = nullStringToPtr(memoryLimit)
	q.StorageLimit = nullStringToPtr(storageLimit)
	q.ExpScript = nullStringToPtr(expScript)
	q.CheckScript = nullStringToPtr(checkScript)
	q.PatchWhitelist = nullStringToPtr(patchWhitelist)
	q.VulnerableFile = nullStringToPtr(vulnerableFile)
	q.FlagEnv = nullStringToPtr(flagEnv)
	q.FlagScript = nullStringToPtr(flagScript)
	q.ImageStatus = nullStringToPtr(imageStatus)
	q.CreatedAt = createdAt.Format(time.RFC3339)
	q.UpdatedAt = updatedAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, q)
}

// HandleUpdateAWDFQuestion 更新AWD-F题库题目
func HandleUpdateAWDFQuestion(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var req UpdateAWDFQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 验证分类是否存在
	if req.CategoryID > 0 {
		var exists bool
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM categories WHERE id = $1)", req.CategoryID).Scan(&exists)
		if !exists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CATEGORY_NOT_FOUND"})
			return
		}
	}

	result, err := db.Exec(`
		UPDATE question_bank_awdf SET
			title = COALESCE(NULLIF($1, ''), title),
			category_id = CASE WHEN $2 = 0 THEN category_id ELSE $2 END,
			difficulty = CASE WHEN $3 = 0 THEN difficulty ELSE $3 END,
			description = $4,
			docker_image = COALESCE(NULLIF($5, ''), docker_image),
			ports = $6,
			cpu_limit = $7,
			memory_limit = $8,
			storage_limit = $9,
			no_resource_limit = $10,
			exp_script = $11,
			check_script = $12,
			patch_whitelist = $13,
			vulnerable_file = $14,
			flag_env = $15,
			flag_script = $16,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $17
	`,
		req.Title, req.CategoryID, req.Difficulty, NullIfEmpty(req.Description), req.DockerImage,
		NullIfEmpty(req.Ports), NullIfEmpty(req.CPULimit),
		NullIfEmpty(req.MemoryLimit), NullIfEmpty(req.StorageLimit), req.NoResourceLimit,
		NullIfEmpty(req.ExpScript), NullIfEmpty(req.CheckScript),
		NullIfEmpty(req.PatchWhitelist), NullIfEmpty(req.VulnerableFile),
		NullIfEmpty(req.FlagEnv), NullIfEmpty(req.FlagScript), id,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "AWD-F question updated"})
}

// HandleDeleteAWDFQuestion 删除AWD-F题库题目
func HandleDeleteAWDFQuestion(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否被比赛使用
	var count int
	db.QueryRow("SELECT COUNT(*) FROM contest_challenges_awdf WHERE question_id = $1", id).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "QUESTION_IN_USE", "count": count})
		return
	}

	result, err := db.Exec("DELETE FROM question_bank_awdf WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "AWD-F question deleted"})
}

// HandleGetAWDFStats 获取AWD-F题库统计
func HandleGetAWDFStats(c *gin.Context, db *sql.DB) {
	var total int
	db.QueryRow("SELECT COUNT(*) FROM question_bank_awdf").Scan(&total)

	c.JSON(http.StatusOK, gin.H{
		"total": total,
	})
}
