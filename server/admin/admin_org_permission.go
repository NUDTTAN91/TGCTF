// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package admin

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// HandleAdminCommonOrgUsers 查看组织成员
func HandleAdminCommonOrgUsers(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")

	// 权限检查
	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.user.view", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 查询该组织下的用户列表（复用 HandleGetOrganizationUsers 的 SQL 逻辑）
	rows, err := db.Query(`
		SELECT u.id, u.username, u.display_name, u.role, COALESCE(u.email, ''),
		       u.status,
		       COALESCE(t.name, '') as team_name,
		       COALESCE(TO_CHAR(u.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at
		FROM users u
		LEFT JOIN teams t ON u.team_id = t.id
		WHERE u.organization_id = $1 AND u.role = 'user'
		ORDER BY u.id ASC`, orgID)
	if err != nil {
		log.Printf("admin common get org users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id int64
		var username, displayName, userRole, email, status, teamName, createdAt string
		if err := rows.Scan(&id, &username, &displayName, &userRole, &email, &status, &teamName, &createdAt); err != nil {
			continue
		}
		users = append(users, gin.H{
			"id":          id,
			"username":    username,
			"displayName": displayName,
			"role":        userRole,
			"email":       email,
			"status":      status,
			"teamName":    teamName,
			"createdAt":   createdAt,
		})
	}

	if users == nil {
		users = []gin.H{}
	}

	// 查询当前管理员对该组织拥有的用户权限
	permissions := getOrgSubPermissions(db, userID, role, orgID, "user")

	c.JSON(http.StatusOK, gin.H{"users": users, "permissions": permissions})
}

// HandleAdminCommonEditOrgUser 编辑组织成员
func HandleAdminCommonEditOrgUser(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")
	targetUserID := c.Param("userId")

	// 权限检查
	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.user.edit", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 验证目标用户属于该组织
	var targetOrgID sql.NullInt64
	var targetRole string
	err := db.QueryRow(`SELECT organization_id, role FROM users WHERE id = $1`, targetUserID).Scan(&targetOrgID, &targetRole)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("query target user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	orgIDInt, _ := strconv.ParseInt(orgID, 10, 64)
	if !targetOrgID.Valid || targetOrgID.Int64 != orgIDInt {
		c.JSON(http.StatusBadRequest, gin.H{"error": "USER_NOT_IN_ORG"})
		return
	}

	// 禁止编辑超管和其他管理员
	if targetRole == "super" || targetRole == "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_EDIT_ADMIN"})
		return
	}

	var req struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var updates []string
	var args []interface{}
	argIndex := 1

	if req.DisplayName != "" {
		updates = append(updates, "display_name = $"+strconv.Itoa(argIndex))
		args = append(args, req.DisplayName)
		argIndex++
	}
	if req.Email != "" {
		updates = append(updates, "email = $"+strconv.Itoa(argIndex))
		args = append(args, req.Email)
		argIndex++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, targetUserID)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIndex)
	_, err = db.Exec(query, args...)
	if err != nil {
		log.Printf("admin common edit org user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED"})
}

// HandleAdminCommonBanOrgUser 禁用/启用组织成员
func HandleAdminCommonBanOrgUser(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")
	targetUserID := c.Param("userId")

	// 权限检查
	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.user.ban", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 验证目标用户属于该组织
	var targetOrgID sql.NullInt64
	var targetRole string
	err := db.QueryRow(`SELECT organization_id, role FROM users WHERE id = $1`, targetUserID).Scan(&targetOrgID, &targetRole)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("query target user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	orgIDInt, _ := strconv.ParseInt(orgID, 10, 64)
	if !targetOrgID.Valid || targetOrgID.Int64 != orgIDInt {
		c.JSON(http.StatusBadRequest, gin.H{"error": "USER_NOT_IN_ORG"})
		return
	}

	// 禁止操作超管和其他管理员
	if targetRole == "super" || targetRole == "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_BAN_ADMIN"})
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if req.Status != "active" && req.Status != "banned" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
		return
	}

	_, err = db.Exec(`UPDATE users SET status = $1, updated_at = NOW() WHERE id = $2`, req.Status, targetUserID)
	if err != nil {
		log.Printf("admin common ban org user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "STATUS_UPDATED"})
}

// HandleAdminCommonOrgTeams 查看组织队伍
func HandleAdminCommonOrgTeams(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")

	// 权限检查
	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.team.view", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 复用 HandleGetOrganizationTeams 的 SQL 逻辑
	rows, err := db.Query(`
		SELECT t.id, t.name, COALESCE(t.description, ''), t.status,
		       t.is_admin_team,
		       (SELECT COUNT(*) FROM users WHERE team_id = t.id) as member_count,
		       COALESCE(u.display_name, '') as captain_name,
		       COALESCE(TO_CHAR(t.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at
		FROM teams t
		LEFT JOIN users u ON t.captain_id = u.id
		WHERE t.organization_id = $1 AND t.is_admin_team = FALSE
		ORDER BY t.id ASC`, orgID)
	if err != nil {
		log.Printf("admin common get org teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var teams []gin.H
	for rows.Next() {
		var id int64
		var name, description, status, captainName, createdAt string
		var isAdminTeam bool
		var memberCount int
		if err := rows.Scan(&id, &name, &description, &status, &isAdminTeam, &memberCount, &captainName, &createdAt); err != nil {
			continue
		}
		teams = append(teams, gin.H{
			"id":          id,
			"name":        name,
			"description": description,
			"status":      status,
			"isAdminTeam": isAdminTeam,
			"memberCount": memberCount,
			"captainName": captainName,
			"createdAt":   createdAt,
		})
	}

	if teams == nil {
		teams = []gin.H{}
	}

	// 查询当前管理员对该组织拥有的队伍权限
	permissions := getOrgSubPermissions(db, userID, role, orgID, "team")

	c.JSON(http.StatusOK, gin.H{"teams": teams, "permissions": permissions})
}

// HandleAdminCommonEditOrgTeam 编辑组织队伍
func HandleAdminCommonEditOrgTeam(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")
	targetTeamID := c.Param("teamId")

	// 权限检查
	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.team.edit", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 验证目标队伍属于该组织
	var teamOrgID sql.NullInt64
	var isAdminTeam bool
	err := db.QueryRow(`SELECT organization_id, is_admin_team FROM teams WHERE id = $1`, targetTeamID).Scan(&teamOrgID, &isAdminTeam)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "TEAM_NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("query target team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	orgIDInt, _ := strconv.ParseInt(orgID, 10, 64)
	if !teamOrgID.Valid || teamOrgID.Int64 != orgIDInt {
		c.JSON(http.StatusBadRequest, gin.H{"error": "TEAM_NOT_IN_ORG"})
		return
	}

	// 禁止编辑管理员队伍
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_EDIT_ADMIN_TEAM"})
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var updates []string
	var args []interface{}
	argIndex := 1

	if req.Name != "" {
		// 检查名称冲突
		var existsID int64
		err := db.QueryRow(`SELECT id FROM teams WHERE name = $1 AND id != $2`, req.Name, targetTeamID).Scan(&existsID)
		if err == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "TEAM_NAME_EXISTS"})
			return
		}
		updates = append(updates, "name = $"+strconv.Itoa(argIndex))
		args = append(args, req.Name)
		argIndex++
	}
	if req.Description != "" {
		updates = append(updates, "description = $"+strconv.Itoa(argIndex))
		args = append(args, req.Description)
		argIndex++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, targetTeamID)

	query := "UPDATE teams SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIndex)
	_, err = db.Exec(query, args...)
	if err != nil {
		log.Printf("admin common edit org team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED"})
}

// HandleAdminCommonBanOrgTeam 禁用/启用组织队伍
func HandleAdminCommonBanOrgTeam(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")
	targetTeamID := c.Param("teamId")

	// 权限检查
	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.team.ban", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 验证目标队伍属于该组织
	var teamOrgID sql.NullInt64
	var isAdminTeam bool
	err := db.QueryRow(`SELECT organization_id, is_admin_team FROM teams WHERE id = $1`, targetTeamID).Scan(&teamOrgID, &isAdminTeam)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "TEAM_NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("query target team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	orgIDInt, _ := strconv.ParseInt(orgID, 10, 64)
	if !teamOrgID.Valid || teamOrgID.Int64 != orgIDInt {
		c.JSON(http.StatusBadRequest, gin.H{"error": "TEAM_NOT_IN_ORG"})
		return
	}

	// 禁止操作管理员队伍
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_BAN_ADMIN_TEAM"})
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if req.Status != "active" && req.Status != "banned" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
		return
	}

	_, err = db.Exec(`UPDATE teams SET status = $1, updated_at = NOW() WHERE id = $2`, req.Status, targetTeamID)
	if err != nil {
		log.Printf("admin common ban org team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "STATUS_UPDATED"})
}

// getOrgSubPermissions 获取管理员对某组织的子权限列表
func getOrgSubPermissions(db *sql.DB, userID int64, role string, orgID string, category string) []string {
	// 超管拥有所有权限
	if role == "super" {
		return []string{category + ".view", category + ".edit", category + ".ban"}
	}

	var perms []string
	subPerms := []string{category + ".view", category + ".edit", category + ".ban"}
	for _, sp := range subPerms {
		perm := fmt.Sprintf("org.%s.%s", orgID, sp)
		if HasPermission(db, userID, role, perm) {
			perms = append(perms, sp)
		}
	}
	return perms
}
