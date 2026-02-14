// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package admin

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// UserDetail 用户详情
type UserDetail struct {
	ID               int64   `json:"id"`
	Username         string  `json:"username"`
	DisplayName      string  `json:"displayName"`
	Email            *string `json:"email"`
	Role             string  `json:"role"`
	Status           string  `json:"status"`
	TeamID           *int64  `json:"teamId"`
	TeamName         *string `json:"teamName"`
	OrganizationID   *int64  `json:"organizationId"`
	OrganizationName *string `json:"organizationName"`
	LastLoginIP      *string `json:"lastLoginIp"`
	LastLoginAt      *string `json:"lastLoginAt"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

// CreateUserRequest 创建用户请求
type CreateUserRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"displayName" binding:"required"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Password    string `json:"password" binding:"required"`
}

// UpdateUserRequest 更新用户请求
type UpdateUserRequest struct {
	DisplayName    string `json:"displayName"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	OrganizationID *int64 `json:"organizationId"` // 支持修改组织，null表示清除组织
}

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	NewPassword string `json:"newPassword" binding:"required"`
}

// HandleListUsers 获取用户列表
func HandleListUsers(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.status, 
		       u.team_id, t.name as team_name, u.organization_id, o.name as org_name,
		       u.last_login_ip, 
		       TO_CHAR(u.last_login_at, 'YYYY-MM-DD HH24:MI') as last_login_at,
		       TO_CHAR(u.created_at, 'YYYY-MM-DD HH24:MI') as created_at,
		       TO_CHAR(u.updated_at, 'YYYY-MM-DD HH24:MI') as updated_at
		FROM users u
		LEFT JOIN teams t ON u.team_id = t.id
		LEFT JOIN organizations o ON u.organization_id = o.id
		ORDER BY u.id ASC`)
	if err != nil {
		log.Printf("list users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var users []UserDetail
	for rows.Next() {
		var u UserDetail
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.Status,
			&u.TeamID, &u.TeamName, &u.OrganizationID, &u.OrganizationName, &u.LastLoginIP, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			log.Printf("scan user error: %v", err)
			continue
		}
		users = append(users, u)
	}

	// 统计
	var total, adminCount, activeToday, bannedCount int64
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&total)
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&adminCount)
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE last_login_at >= CURRENT_DATE`).Scan(&activeToday)
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE status = 'banned'`).Scan(&bannedCount)

	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"stats": gin.H{
			"total":       total,
			"adminCount":  adminCount,
			"activeToday": activeToday,
			"bannedCount": bannedCount,
		},
	})
}

// HandleCreateUser 创建用户
func HandleCreateUser(c *gin.Context, db *sql.DB) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查用户名是否已存在
	var exists int
	db.QueryRow(`SELECT 1 FROM users WHERE username = $1`, req.Username).Scan(&exists)
	if exists == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "USERNAME_EXISTS"})
		return
	}

	// 加密密码
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("hash password error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	role := req.Role
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "admin" && role != "super" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ROLE"})
		return
	}

	var email *string
	if req.Email != "" {
		email = &req.Email
	}

	var id int64
	err = db.QueryRow(`INSERT INTO users (username, display_name, email, role, password_hash, status, created_at, updated_at) 
		VALUES ($1, $2, $3, $4, $5, 'active', NOW(), NOW()) RETURNING id`,
		req.Username, req.DisplayName, email, role, string(hash)).Scan(&id)
	if err != nil {
		log.Printf("create user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "message": "CREATED"})
}

// HandleGetUser 获取单个用户
func HandleGetUser(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var u UserDetail
	err := db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.status, 
		       u.team_id, t.name as team_name, u.organization_id, o.name as org_name,
		       u.last_login_ip, 
		       TO_CHAR(u.last_login_at, 'YYYY-MM-DD HH24:MI') as last_login_at,
		       TO_CHAR(u.created_at, 'YYYY-MM-DD HH24:MI') as created_at,
		       TO_CHAR(u.updated_at, 'YYYY-MM-DD HH24:MI') as updated_at
		FROM users u
		LEFT JOIN teams t ON u.team_id = t.id
		LEFT JOIN organizations o ON u.organization_id = o.id
		WHERE u.id = $1`, id).Scan(
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.Status,
		&u.TeamID, &u.TeamName, &u.OrganizationID, &u.OrganizationName, &u.LastLoginIP, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("get user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, u)
}

// HandleUpdateUser 更新用户
func HandleUpdateUser(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 使用 map 解析以区分“未传递”和“传递null”
	var rawReq map[string]interface{}
	if err := c.ShouldBindJSON(&rawReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var updates []string
	var args []interface{}
	argIndex := 1

	if displayName, ok := rawReq["displayName"].(string); ok && displayName != "" {
		updates = append(updates, "display_name = $"+strconv.Itoa(argIndex))
		args = append(args, displayName)
		argIndex++
	}
	if email, ok := rawReq["email"].(string); ok && email != "" {
		updates = append(updates, "email = $"+strconv.Itoa(argIndex))
		args = append(args, email)
		argIndex++
	}
	if role, ok := rawReq["role"].(string); ok && role != "" {
		if role != "user" && role != "admin" && role != "super" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ROLE"})
			return
		}
		updates = append(updates, "role = $"+strconv.Itoa(argIndex))
		args = append(args, role)
		argIndex++
	}
	if status, ok := rawReq["status"].(string); ok && status != "" {
		if status != "active" && status != "banned" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
			return
		}
		updates = append(updates, "status = $"+strconv.Itoa(argIndex))
		args = append(args, status)
		argIndex++
	}

	// 处理组织ID：区分“未传递”和“传递null”
	if _, exists := rawReq["organizationId"]; exists {
		if rawReq["organizationId"] == nil {
			// 明确传递null，清除组织
			updates = append(updates, "organization_id = NULL")
		} else if orgIDFloat, ok := rawReq["organizationId"].(float64); ok {
			orgID := int64(orgIDFloat)
			// 检查组织是否存在
			var orgExists int
			db.QueryRow(`SELECT 1 FROM organizations WHERE id = $1`, orgID).Scan(&orgExists)
			if orgExists != 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "ORGANIZATION_NOT_FOUND"})
				return
			}
			updates = append(updates, "organization_id = $"+strconv.Itoa(argIndex))
			args = append(args, orgID)
			argIndex++
		}
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, id)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIndex)
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("update user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED"})
}

// HandleDeleteUser 删除用户
func HandleDeleteUser(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	result, err := db.Exec(`DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		log.Printf("delete user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "DELETED"})
}

// HandleResetPassword 重置密码（默认重置为123456）
func HandleResetPassword(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 默认密码 123456
	defaultPassword := "123456"
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("hash password error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 重置密码并设置强制修改密码标记，同时递增 token_version 使旧登录失效
	result, err := db.Exec(`UPDATE users SET password_hash = $1, must_change_password = TRUE, token_version = COALESCE(token_version, 1) + 1, updated_at = NOW() WHERE id = $2`, string(hash), id)
	if err != nil {
		log.Printf("reset password error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "PASSWORD_RESET"})
}

// BatchUserRequest 批量操作请求
type BatchUserRequest struct {
	UserIDs []int64 `json:"userIds" binding:"required"`
}

// HandleBatchBanUsers 批量封禁用户
func HandleBatchBanUsers(c *gin.Context, db *sql.DB) {
	var req BatchUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_USERS_SELECTED"})
		return
	}

	// 构建批量更新SQL（排除超级管理员）
	placeholders := make([]string, len(req.UserIDs))
	args := make([]interface{}, len(req.UserIDs))
	for i, id := range req.UserIDs {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = id
	}

	query := `UPDATE users SET status = 'banned', updated_at = NOW() WHERE id IN (` + strings.Join(placeholders, ",") + `) AND role != 'super'`
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("batch ban users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	c.JSON(http.StatusOK, gin.H{"message": "BATCH_BANNED", "count": rowsAffected})
}

// HandleBatchUnbanUsers 批量解封用户
func HandleBatchUnbanUsers(c *gin.Context, db *sql.DB) {
	var req BatchUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_USERS_SELECTED"})
		return
	}

	placeholders := make([]string, len(req.UserIDs))
	args := make([]interface{}, len(req.UserIDs))
	for i, id := range req.UserIDs {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = id
	}

	query := `UPDATE users SET status = 'active', updated_at = NOW() WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("batch unban users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	c.JSON(http.StatusOK, gin.H{"message": "BATCH_UNBANNED", "count": rowsAffected})
}

// HandleBatchResetPasswords 批量重置密码
func HandleBatchResetPasswords(c *gin.Context, db *sql.DB) {
	var req BatchUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_USERS_SELECTED"})
		return
	}

	// 默认密码 123456
	defaultPassword := "123456"
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("hash password error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 构建批量更新SQL（排除超级管理员）
	placeholders := make([]string, len(req.UserIDs))
	args := make([]interface{}, len(req.UserIDs)+1)
	args[0] = string(hash)
	for i, id := range req.UserIDs {
		placeholders[i] = "$" + strconv.Itoa(i+2)
		args[i+1] = id
	}

	query := `UPDATE users SET password_hash = $1, must_change_password = TRUE, token_version = COALESCE(token_version, 1) + 1, updated_at = NOW() WHERE id IN (` + strings.Join(placeholders, ",") + `) AND role != 'super'`
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("batch reset passwords error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	c.JSON(http.StatusOK, gin.H{"message": "BATCH_PASSWORD_RESET", "count": rowsAffected})
}

// HandleSetUserTeam 设置用户加入队伍
func HandleSetUserTeam(c *gin.Context, db *sql.DB) {
	userID := c.Param("id")

	var req struct {
		TeamID *int64 `json:"teamId"` // 为nil表示退出队伍
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查用户是否存在且不是超级管理员
	var userRole string
	err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, userID).Scan(&userRole)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if userRole == "super" {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_MODIFY_SUPER_ADMIN", "message": "不能修改超级管理员的队伍"})
		return
	}

	// 如果要加入队伍，检查队伍是否存在且不是管理员队伍
	if req.TeamID != nil {
		var isAdminTeam bool
		err := db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, *req.TeamID).Scan(&isAdminTeam)
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "TEAM_NOT_FOUND"})
			return
		}
		if isAdminTeam {
			c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_JOIN_ADMIN_TEAM", "message": "不能加入管理员队伍"})
			return
		}

		// 获取队伍的组织ID，同步更新用户的组织
		var teamOrgID sql.NullInt64
		db.QueryRow(`SELECT organization_id FROM teams WHERE id = $1`, *req.TeamID).Scan(&teamOrgID)

		if teamOrgID.Valid {
			_, err = db.Exec(`UPDATE users SET team_id = $1, organization_id = $2, updated_at = NOW() WHERE id = $3`, *req.TeamID, teamOrgID.Int64, userID)
		} else {
			_, err = db.Exec(`UPDATE users SET team_id = $1, organization_id = NULL, updated_at = NOW() WHERE id = $2`, *req.TeamID, userID)
		}
	} else {
		// 退出队伍，同时清除组织
		_, err = db.Exec(`UPDATE users SET team_id = NULL, organization_id = NULL, updated_at = NOW() WHERE id = $1`, userID)
	}

	if err != nil {
		log.Printf("set user team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "TEAM_SET"})
}

// HandleGetAllTeams 获取所有队伍列表（用于下拉选择）
func HandleGetAllTeams(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT t.id, t.name, COALESCE(o.name, '') as org_name
		FROM teams t
		LEFT JOIN organizations o ON t.organization_id = o.id
		WHERE t.is_admin_team = FALSE AND t.status = 'active'
		ORDER BY t.name ASC`)
	if err != nil {
		log.Printf("get all teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var teams []gin.H
	for rows.Next() {
		var id int64
		var name, orgName string
		if err := rows.Scan(&id, &name, &orgName); err != nil {
			continue
		}
		teams = append(teams, gin.H{
			"id":      id,
			"name":    name,
			"orgName": orgName,
		})
	}

	if teams == nil {
		teams = []gin.H{}
	}
	c.JSON(http.StatusOK, teams)
}
