// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

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

// HandleAdminCommonOrgDockerInstances 查看组织范围的 Docker 实例列表
func HandleAdminCommonOrgDockerInstances(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")

	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.docker.view", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	status := c.Query("status")
	search := c.Query("search")

	baseQuery := `
		SELECT * FROM (
			SELECT
				ti.id, ti.container_id, ti.container_name,
				ti.team_id, COALESCE(t.name, '无队伍') as team_name,
				ti.contest_id, COALESCE(ct.name, '未知比赛') as contest_name,
				ti.challenge_id, COALESCE(q.title, '未知题目') as challenge_name,
				COALESCE(ti.created_by, 0), COALESCE(u.display_name, '-') as user_name,
				ti.ports, ti.status, ti.expires_at, ti.created_at
			FROM team_instances ti
			LEFT JOIN teams t ON ti.team_id = t.id
			LEFT JOIN contests ct ON ti.contest_id = ct.id
			LEFT JOIN contest_challenges cc ON ti.challenge_id = cc.id
			LEFT JOIN question_bank q ON cc.question_id = q.id
			LEFT JOIN users u ON ti.created_by = u.id
			WHERE u.organization_id = $1
			UNION ALL
			SELECT
				tia.id, tia.container_id, tia.container_name,
				tia.team_id, COALESCE(t.name, '无队伍') as team_name,
				tia.contest_id, COALESCE(ct.name, '未知比赛') as contest_name,
				tia.challenge_id, COALESCE(qa.title, '未知题目') as challenge_name,
				COALESCE(tia.created_by, 0), COALESCE(u.display_name, '系统') as user_name,
				tia.ports, tia.status, tia.expires_at, tia.created_at
			FROM team_instances_awdf tia
			LEFT JOIN teams t ON tia.team_id = t.id
			LEFT JOIN contests ct ON tia.contest_id = ct.id
			LEFT JOIN contest_challenges_awdf cca ON tia.challenge_id = cca.id
			LEFT JOIN question_bank_awdf qa ON cca.question_id = qa.id
			LEFT JOIN users u ON tia.created_by = u.id
			WHERE u.organization_id = $1
		) combined WHERE 1=1
	`
	args := []interface{}{orgID}
	argIdx := 2

	if status == "" || status == "running" {
		baseQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, "running")
		argIdx++
	}

	if search != "" {
		baseQuery += fmt.Sprintf(" AND (container_name ILIKE $%d OR user_name ILIKE $%d OR team_name ILIKE $%d)", argIdx, argIdx+1, argIdx+2)
		searchPattern := "%" + search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
		argIdx += 3
	}

	baseQuery += " ORDER BY created_at DESC"

	rows, err := db.Query(baseQuery, args...)
	if err != nil {
		log.Printf("admin common get org docker instances error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var instances []gin.H
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)

	for rows.Next() {
		var id, teamID, contestID, challengeID, createdBy int64
		var containerID, containerName, teamName, contestName, challengeName, userName, portsJSON, instStatus string
		var expiresAt, createdAt time.Time
		err := rows.Scan(&id, &containerID, &containerName,
			&teamID, &teamName, &contestID, &contestName,
			&challengeID, &challengeName, &createdBy, &userName,
			&portsJSON, &instStatus, &expiresAt, &createdAt)
		if err != nil {
			continue
		}
		var ports map[string]string
		json.Unmarshal([]byte(portsJSON), &ports)
		instances = append(instances, gin.H{
			"id":            id,
			"containerId":   containerID,
			"containerName": containerName,
			"teamId":        teamID,
			"teamName":      teamName,
			"contestId":     contestID,
			"contestName":   contestName,
			"challengeId":   challengeID,
			"challengeName": challengeName,
			"userId":        createdBy,
			"userName":      userName,
			"ports":         ports,
			"status":        instStatus,
			"expiresAt":     expiresAt.Format("2006-01-02 15:04:05"),
			"createdAt":     createdAt.Format("2006-01-02 15:04:05"),
			"isExpired":     now.After(expiresAt),
		})
	}

	if instances == nil {
		instances = []gin.H{}
	}

	permissions := getOrgSubPermissions(db, userID, role, orgID, "docker")

	c.JSON(http.StatusOK, gin.H{"instances": instances, "total": len(instances), "permissions": permissions})
}

// HandleAdminCommonDestroyOrgDockerInstance 销毁组织范围的 Docker 实例
func HandleAdminCommonDestroyOrgDockerInstance(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")
	instanceID := c.Param("instanceId")

	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.docker.delete", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	// 查询实例信息，验证用户属于该组织
	var containerID string
	var createdBy sql.NullInt64
	err := db.QueryRow(`SELECT ti.container_id, ti.created_by FROM team_instances ti WHERE ti.id = $1`, instanceID).Scan(&containerID, &createdBy)
	if err != nil {
		// 尝试 awdf 表
		err = db.QueryRow(`SELECT tia.container_id, tia.created_by FROM team_instances_awdf tia WHERE tia.id = $1`, instanceID).Scan(&containerID, &createdBy)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "INSTANCE_NOT_FOUND"})
			return
		}
	}

	// 验证创建者属于该组织
	if createdBy.Valid {
		var userOrgID sql.NullInt64
		db.QueryRow(`SELECT organization_id FROM users WHERE id = $1`, createdBy.Int64).Scan(&userOrgID)
		orgIDInt, _ := strconv.ParseInt(orgID, 10, 64)
		if !userOrgID.Valid || userOrgID.Int64 != orgIDInt {
			c.JSON(http.StatusForbidden, gin.H{"error": "INSTANCE_NOT_IN_ORG"})
			return
		}
	} else {
		c.JSON(http.StatusForbidden, gin.H{"error": "INSTANCE_NOT_IN_ORG"})
		return
	}

	// 停止并删除容器
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()

	// 更新数据库状态（两张表都尝试更新）
	db.Exec(`UPDATE team_instances SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, instanceID)
	db.Exec(`UPDATE team_instances_awdf SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, instanceID)

	c.JSON(http.StatusOK, gin.H{"message": "容器已销毁"})
}

// HandleAdminCommonOrgDockerStats 获取组织范围的 Docker 统计
func HandleAdminCommonOrgDockerStats(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")

	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.docker.view", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	var runningCount, expiredCount, todayCreated, todayDestroyed int

	db.QueryRow(`
		SELECT COUNT(*) FROM team_instances ti
		JOIN users u ON ti.created_by = u.id
		WHERE u.organization_id = $1 AND ti.status = 'running'`, orgID).Scan(&runningCount)

	db.QueryRow(`
		SELECT COUNT(*) FROM team_instances ti
		JOIN users u ON ti.created_by = u.id
		WHERE u.organization_id = $1 AND ti.status = 'running' AND ti.expires_at < CURRENT_TIMESTAMP`, orgID).Scan(&expiredCount)

	db.QueryRow(`
		SELECT COUNT(*) FROM team_instances ti
		JOIN users u ON ti.created_by = u.id
		WHERE u.organization_id = $1 AND ti.created_at >= CURRENT_DATE`, orgID).Scan(&todayCreated)

	db.QueryRow(`
		SELECT COUNT(*) FROM team_instances ti
		JOIN users u ON ti.created_by = u.id
		WHERE u.organization_id = $1 AND ti.status = 'destroyed' AND ti.updated_at >= CURRENT_DATE`, orgID).Scan(&todayDestroyed)

	c.JSON(http.StatusOK, gin.H{
		"runningCount":   runningCount,
		"expiredCount":   expiredCount,
		"todayCreated":   todayCreated,
		"todayDestroyed": todayDestroyed,
	})
}

// HandleAdminCommonOrgAntiCheat 获取组织范围的防作弊记录（简化版：仅 same_ip_diff_team）
func HandleAdminCommonOrgAntiCheat(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")

	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.anticheat.view", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	contestID := c.Query("contestId")

	// 检测同IP不同队伍提交正确答案（高风险），仅限该组织用户
	sameIPQuery := `
		SELECT s.ip_address, s.challenge_id, cc.contest_id,
		       COALESCE(q.title, cc.inline_title, '') as challenge_name, c.name as contest_name,
		       COUNT(DISTINCT s.team_id) as team_count
		FROM submissions s
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		LEFT JOIN question_bank q ON cc.question_id = q.id
		JOIN contests c ON cc.contest_id = c.id
		JOIN users u2 ON s.user_id = u2.id
		WHERE s.is_correct = true AND s.ip_address IS NOT NULL AND s.ip_address != ''
		  AND u2.organization_id = $1
	`
	args := []interface{}{orgID}
	argIdx := 2

	if contestID != "" {
		sameIPQuery += fmt.Sprintf(" AND cc.contest_id = $%d", argIdx)
		args = append(args, contestID)
		argIdx++
	}

	sameIPQuery += `
		GROUP BY s.ip_address, s.challenge_id, cc.contest_id, q.title, cc.inline_title, c.name
		HAVING COUNT(DISTINCT s.team_id) > 1
		ORDER BY team_count DESC
		LIMIT 50
	`

	rows, err := db.Query(sameIPQuery, args...)
	if err != nil {
		log.Printf("admin common org anti-cheat error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var records []gin.H
	for rows.Next() {
		var ip string
		var challengeID, cID int64
		var challengeName, contestName string
		var teamCount int
		rows.Scan(&ip, &challengeID, &cID, &challengeName, &contestName, &teamCount)

		// 获取详情
		detailRows, dErr := db.Query(`
			SELECT s.team_id, t.name, s.user_id, COALESCE(u.display_name, u.username),
			       s.ip_address, s.flag, s.is_correct, s.submitted_at
			FROM submissions s
			JOIN teams t ON s.team_id = t.id
			JOIN users u ON s.user_id = u.id
			WHERE s.ip_address = $1 AND s.challenge_id = $2 AND s.is_correct = true
			  AND u.organization_id = $3
			ORDER BY s.submitted_at
		`, ip, challengeID, orgID)

		var details []gin.H
		if dErr == nil {
			for detailRows.Next() {
				var teamIDd, userIDd int64
				var teamNamed, userNamed, ipAddr, flag string
				var isCorrect bool
				var submittedAt string
				detailRows.Scan(&teamIDd, &teamNamed, &userIDd, &userNamed, &ipAddr, &flag, &isCorrect, &submittedAt)
				details = append(details, gin.H{
					"teamId":      teamIDd,
					"teamName":    teamNamed,
					"userId":      userIDd,
					"userName":    userNamed,
					"ipAddress":   ipAddr,
					"flag":        flag,
					"isCorrect":   isCorrect,
					"submittedAt": submittedAt,
				})
			}
			detailRows.Close()
		}

		if details == nil {
			details = []gin.H{}
		}

		records = append(records, gin.H{
			"type":          "same_ip_diff_team",
			"riskLevel":     "high",
			"contestId":     cID,
			"contestName":   contestName,
			"challengeId":   challengeID,
			"challengeName": challengeName,
			"description":   "同一IP(" + ip + ")有" + strconv.Itoa(teamCount) + "个不同队伍提交了正确答案",
			"details":       details,
		})
	}

	if records == nil {
		records = []gin.H{}
	}

	c.JSON(http.StatusOK, records)
}

// HandleAdminCommonOrgLogs 获取组织范围的系统日志
func HandleAdminCommonOrgLogs(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")
	orgID := c.Param("id")

	if !HasPermission(db, userID, role, fmt.Sprintf("org.%s.logs.view", orgID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_PERMISSION"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 10 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	logType := c.Query("type")
	level := c.Query("level")
	search := c.Query("search")

	query := `
		SELECT l.id, l.type, l.level, l.user_id, COALESCE(u.display_name, u.username), l.team_id, t.name,
		       l.contest_id, l.challenge_id, l.ip_address, l.message, l.details, l.created_at
		FROM system_logs l
		JOIN users u ON l.user_id = u.id
		LEFT JOIN teams t ON l.team_id = t.id
		WHERE u.organization_id = $1`
	countQuery := `
		SELECT COUNT(*) FROM system_logs l
		JOIN users u ON l.user_id = u.id
		WHERE u.organization_id = $1`
	args := []interface{}{orgID}
	argIdx := 2

	if logType != "" {
		query += " AND l.type = $" + strconv.Itoa(argIdx)
		countQuery += " AND l.type = $" + strconv.Itoa(argIdx)
		args = append(args, logType)
		argIdx++
	}
	if level != "" {
		query += " AND l.level = $" + strconv.Itoa(argIdx)
		countQuery += " AND l.level = $" + strconv.Itoa(argIdx)
		args = append(args, level)
		argIdx++
	}
	if search != "" {
		query += " AND l.message ILIKE $" + strconv.Itoa(argIdx)
		countQuery += " AND l.message ILIKE $" + strconv.Itoa(argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	db.QueryRow(countQuery, countArgs...).Scan(&total)

	query += " ORDER BY l.created_at DESC LIMIT $" + strconv.Itoa(argIdx) + " OFFSET $" + strconv.Itoa(argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("admin common org logs error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var logEntries []gin.H
	for rows.Next() {
		var id int64
		var lType, lLevel, message string
		var luserID, teamID, contestID, challengeID sql.NullInt64
		var userName, teamName, ipAddress sql.NullString
		var details []byte
		var createdAt time.Time

		if err := rows.Scan(&id, &lType, &lLevel, &luserID, &userName, &teamID, &teamName,
			&contestID, &challengeID, &ipAddress, &message, &details, &createdAt); err != nil {
			continue
		}

		entry := gin.H{
			"id":        id,
			"type":      lType,
			"level":     lLevel,
			"message":   message,
			"createdAt": createdAt.Format("2006-01-02 15:04:05"),
		}
		if luserID.Valid {
			entry["userId"] = luserID.Int64
		}
		if userName.Valid {
			entry["userName"] = userName.String
		}
		if teamID.Valid {
			entry["teamId"] = teamID.Int64
		}
		if teamName.Valid {
			entry["teamName"] = teamName.String
		}
		if contestID.Valid {
			entry["contestId"] = contestID.Int64
		}
		if challengeID.Valid {
			entry["challengeId"] = challengeID.Int64
		}
		if ipAddress.Valid {
			entry["ipAddress"] = ipAddress.String
		}
		if len(details) > 0 {
			entry["details"] = json.RawMessage(details)
		}

		logEntries = append(logEntries, entry)
	}

	if logEntries == nil {
		logEntries = []gin.H{}
	}

	totalPages := (total + pageSize - 1) / pageSize
	c.JSON(http.StatusOK, gin.H{
		"logs":       logEntries,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": totalPages,
	})
}

// getOrgSubPermissions 获取管理员对某组织的子权限列表
func getOrgSubPermissions(db *sql.DB, userID int64, role string, orgID string, category string) []string {
	// 根据类别定义可用的子权限
	var subPermSuffixes []string
	switch category {
	case "docker":
		subPermSuffixes = []string{"view", "delete"}
	case "anticheat", "logs":
		subPermSuffixes = []string{"view"}
	default:
		// user, team 等默认 view/edit/ban
		subPermSuffixes = []string{"view", "edit", "ban"}
	}

	// 超管拥有所有权限
	if role == "super" {
		result := make([]string, len(subPermSuffixes))
		for i, s := range subPermSuffixes {
			result[i] = category + "." + s
		}
		return result
	}

	var perms []string
	for _, s := range subPermSuffixes {
		sp := category + "." + s
		perm := fmt.Sprintf("org.%s.%s", orgID, sp)
		if HasPermission(db, userID, role, perm) {
			perms = append(perms, sp)
		}
	}
	return perms
}
