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

// QuestionBank 题库题目
type QuestionBank struct {
	ID              int64   `json:"id"`
	Title           string  `json:"title"`
	Type            string  `json:"type"`
	CategoryID      int64   `json:"categoryId"`
	CategoryName    *string `json:"categoryName,omitempty"`
	Difficulty      int     `json:"difficulty"`
	Description     *string `json:"description"`
	Flag            *string `json:"flag,omitempty"`
	FlagType        string  `json:"flagType"`
	DockerImage     *string `json:"dockerImage"`
	AttachmentURL   *string `json:"attachmentUrl"`
	AttachmentType  string  `json:"attachmentType"`
	Ports           *string `json:"ports"`
	CPULimit        *string `json:"cpuLimit"`
	MemoryLimit     *string `json:"memoryLimit"`
	StorageLimit    *string `json:"storageLimit"`
	NoResourceLimit bool    `json:"noResourceLimit"`
	FlagEnv         *string `json:"flagEnv"`
	FlagScript      *string `json:"flagScript"`
	NeedsEdit       bool    `json:"needsEdit"`
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

// CreateQuestionRequest 创建题目请求
type CreateQuestionRequest struct {
	Title           string `json:"title" binding:"required"`
	Type            string `json:"type" binding:"required"`
	CategoryID      int64  `json:"categoryId" binding:"required"`
	Difficulty      int    `json:"difficulty"`
	Description     string `json:"description"`
	Flag            string `json:"flag"`
	FlagType        string `json:"flagType"`
	DockerImage     string `json:"dockerImage"`
	AttachmentURL   string `json:"attachmentUrl"`
	AttachmentType  string `json:"attachmentType"`
	Ports           string `json:"ports"`
	CPULimit        string `json:"cpuLimit"`
	MemoryLimit     string `json:"memoryLimit"`
	StorageLimit    string `json:"storageLimit"`
	NoResourceLimit bool   `json:"noResourceLimit"`
	FlagEnv         string `json:"flagEnv"`
	FlagScript      string `json:"flagScript"`
}

// UpdateQuestionRequest 更新题目请求
type UpdateQuestionRequest struct {
	Title           string `json:"title"`
	Type            string `json:"type"`
	CategoryID      int64  `json:"categoryId"`
	Difficulty      int    `json:"difficulty"`
	Description     string `json:"description"`
	Flag            string `json:"flag"`
	FlagType        string `json:"flagType"`
	DockerImage     string `json:"dockerImage"`
	AttachmentURL   string `json:"attachmentUrl"`
	AttachmentType  string `json:"attachmentType"`
	Ports           string `json:"ports"`
	CPULimit        string `json:"cpuLimit"`
	MemoryLimit     string `json:"memoryLimit"`
	StorageLimit    string `json:"storageLimit"`
	NoResourceLimit bool   `json:"noResourceLimit"`
	FlagEnv         string `json:"flagEnv"`
	FlagScript      string `json:"flagScript"`
}

// NullIfEmpty 如果字符串为空返回nil
func NullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// HandleListQuestions 获取题库列表
func HandleListQuestions(c *gin.Context, db *sql.DB) {
	categoryID := c.Query("categoryId")
	questionType := c.Query("type")
	difficulty := c.Query("difficulty")

	query := `
		SELECT q.id, q.title, q.type, q.category_id, c.name as category_name,
			q.difficulty, q.description, q.flag, q.flag_type,
			q.docker_image, q.attachment_url, q.attachment_type, q.ports,
			q.cpu_limit, q.memory_limit, q.storage_limit, q.no_resource_limit, q.flag_env, q.flag_script,
			COALESCE(q.needs_edit, false), q.image_status,
			q.created_at, q.updated_at
		FROM question_bank q
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
	if questionType != "" {
		query += " AND q.type = $" + string(rune('0'+argIndex))
		args = append(args, questionType)
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

	var questions []QuestionBank
	for rows.Next() {
		var q QuestionBank
		var createdAt, updatedAt time.Time
		var categoryName, description, flag, dockerImage, attachmentURL, ports sql.NullString
		var cpuLimit, memoryLimit, storageLimit, flagEnv, flagScript, imageStatus sql.NullString
		err := rows.Scan(
			&q.ID, &q.Title, &q.Type, &q.CategoryID, &categoryName,
			&q.Difficulty, &description, &flag, &q.FlagType,
			&dockerImage, &attachmentURL, &q.AttachmentType, &ports,
			&cpuLimit, &memoryLimit, &storageLimit, &q.NoResourceLimit, &flagEnv, &flagScript,
			&q.NeedsEdit, &imageStatus,
			&createdAt, &updatedAt,
		)
		if err != nil {
			continue
		}
		q.CategoryName = nullStringToPtr(categoryName)
		q.Description = nullStringToPtr(description)
		q.Flag = nullStringToPtr(flag)
		q.DockerImage = nullStringToPtr(dockerImage)
		q.AttachmentURL = nullStringToPtr(attachmentURL)
		q.Ports = nullStringToPtr(ports)
		q.CPULimit = nullStringToPtr(cpuLimit)
		q.MemoryLimit = nullStringToPtr(memoryLimit)
		q.StorageLimit = nullStringToPtr(storageLimit)
		q.FlagEnv = nullStringToPtr(flagEnv)
		q.FlagScript = nullStringToPtr(flagScript)
		q.ImageStatus = nullStringToPtr(imageStatus)
		q.CreatedAt = createdAt.Format(time.RFC3339)
		q.UpdatedAt = updatedAt.Format(time.RFC3339)
		questions = append(questions, q)
	}

	if questions == nil {
		questions = []QuestionBank{}
	}

	c.JSON(http.StatusOK, questions)
}

// HandleCreateQuestion 创建题库题目
func HandleCreateQuestion(c *gin.Context, db *sql.DB) {
	var req CreateQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "details": err.Error()})
		return
	}

	validTypes := map[string]bool{
		"static_attachment":  true,
		"static_container":   true,
		"dynamic_attachment": true,
		"dynamic_container":  true,
	}
	if !validTypes[req.Type] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_TYPE"})
		return
	}

	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM categories WHERE id = $1)", req.CategoryID).Scan(&exists)
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CATEGORY_NOT_FOUND"})
		return
	}

	if req.Difficulty == 0 {
		req.Difficulty = 5
	}
	if req.FlagType == "" {
		req.FlagType = "static"
	}
	if req.AttachmentType == "" {
		req.AttachmentType = "url"
	}

	var id int64
	err := db.QueryRow(`
		INSERT INTO question_bank (
			title, type, category_id, difficulty, description,
			flag, flag_type, docker_image, attachment_url, attachment_type,
			ports, cpu_limit, memory_limit, storage_limit, no_resource_limit, flag_env, flag_script
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id
	`,
		req.Title, req.Type, req.CategoryID, req.Difficulty, req.Description,
		NullIfEmpty(req.Flag), req.FlagType,
		NullIfEmpty(req.DockerImage), NullIfEmpty(req.AttachmentURL), req.AttachmentType,
		NullIfEmpty(req.Ports), NullIfEmpty(req.CPULimit),
		NullIfEmpty(req.MemoryLimit), NullIfEmpty(req.StorageLimit),
		req.NoResourceLimit, NullIfEmpty(req.FlagEnv), NullIfEmpty(req.FlagScript),
	).Scan(&id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Question created"})
}

// HandleGetQuestion 获取单个题库题目
func HandleGetQuestion(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var q QuestionBank
	var createdAt, updatedAt time.Time
	var categoryName, description, flag, dockerImage, attachmentURL, ports sql.NullString
	var cpuLimit, memoryLimit, storageLimit, flagEnv, flagScript, imageStatus sql.NullString
	err := db.QueryRow(`
		SELECT q.id, q.title, q.type, q.category_id, c.name as category_name,
			q.difficulty, q.description, q.flag, q.flag_type,
			q.docker_image, q.attachment_url, q.attachment_type, q.ports,
			q.cpu_limit, q.memory_limit, q.storage_limit, q.no_resource_limit, q.flag_env, q.flag_script,
			COALESCE(q.needs_edit, false), q.image_status,
			q.created_at, q.updated_at
		FROM question_bank q
		LEFT JOIN categories c ON q.category_id = c.id
		WHERE q.id = $1
	`, id).Scan(
		&q.ID, &q.Title, &q.Type, &q.CategoryID, &categoryName,
		&q.Difficulty, &description, &flag, &q.FlagType,
		&dockerImage, &attachmentURL, &q.AttachmentType, &ports,
		&cpuLimit, &memoryLimit, &storageLimit, &q.NoResourceLimit, &flagEnv, &flagScript,
		&q.NeedsEdit, &imageStatus,
		&createdAt, &updatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	q.CategoryName = nullStringToPtr(categoryName)
	q.Description = nullStringToPtr(description)
	q.Flag = nullStringToPtr(flag)
	q.DockerImage = nullStringToPtr(dockerImage)
	q.AttachmentURL = nullStringToPtr(attachmentURL)
	q.Ports = nullStringToPtr(ports)
	q.CPULimit = nullStringToPtr(cpuLimit)
	q.MemoryLimit = nullStringToPtr(memoryLimit)
	q.StorageLimit = nullStringToPtr(storageLimit)
	q.FlagEnv = nullStringToPtr(flagEnv)
	q.FlagScript = nullStringToPtr(flagScript)
	q.ImageStatus = nullStringToPtr(imageStatus)
	q.CreatedAt = createdAt.Format(time.RFC3339)
	q.UpdatedAt = updatedAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, q)
}

// HandleUpdateQuestion 更新题库题目
func HandleUpdateQuestion(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var req UpdateQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if req.Type != "" {
		validTypes := map[string]bool{
			"static_attachment":  true,
			"static_container":   true,
			"dynamic_attachment": true,
			"dynamic_container":  true,
		}
		if !validTypes[req.Type] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_TYPE"})
			return
		}
	}

	if req.CategoryID > 0 {
		var exists bool
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM categories WHERE id = $1)", req.CategoryID).Scan(&exists)
		if !exists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CATEGORY_NOT_FOUND"})
			return
		}
	}

	result, err := db.Exec(`
		UPDATE question_bank SET
			title = COALESCE(NULLIF($1, ''), title),
			type = COALESCE(NULLIF($2, ''), type),
			category_id = CASE WHEN $3 = 0 THEN category_id ELSE $3 END,
			difficulty = CASE WHEN $4 = 0 THEN difficulty ELSE $4 END,
			description = COALESCE(NULLIF($5, ''), description),
			flag = CASE WHEN $6 = '' THEN flag ELSE $6 END,
			flag_type = COALESCE(NULLIF($7, ''), flag_type),
			docker_image = CASE WHEN $8 = '' THEN docker_image ELSE $8 END,
			attachment_url = CASE WHEN $9 = '' THEN attachment_url ELSE $9 END,
			attachment_type = COALESCE(NULLIF($10, ''), attachment_type),
			ports = CASE WHEN $11 = '' THEN ports ELSE $11 END,
			cpu_limit = CASE WHEN $12 = '' THEN cpu_limit ELSE $12 END,
			memory_limit = CASE WHEN $13 = '' THEN memory_limit ELSE $13 END,
			storage_limit = CASE WHEN $14 = '' THEN storage_limit ELSE $14 END,
			no_resource_limit = $15,
			flag_env = CASE WHEN $16 = '' THEN flag_env ELSE $16 END,
			flag_script = $17,
			needs_edit = false,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $18
	`,
		req.Title, req.Type, req.CategoryID, req.Difficulty, req.Description,
		req.Flag, req.FlagType, req.DockerImage, req.AttachmentURL, req.AttachmentType,
		req.Ports, req.CPULimit, req.MemoryLimit, req.StorageLimit,
		req.NoResourceLimit, req.FlagEnv, NullIfEmpty(req.FlagScript), id,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Question updated"})
}

// HandleDeleteQuestion 删除题库题目
func HandleDeleteQuestion(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var count int
	db.QueryRow("SELECT COUNT(*) FROM contest_challenges WHERE question_id = $1", id).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "QUESTION_IN_USE", "count": count})
		return
	}

	result, err := db.Exec("DELETE FROM question_bank WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Question deleted"})
}
