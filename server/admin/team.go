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
)

// TeamDetail 队伍详情
type TeamDetail struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CaptainID   *int64 `json:"captainId"`
	CaptainName string `json:"captainName"`
	IsAdminTeam bool   `json:"isAdminTeam"`
	Status      string `json:"status"`
	MemberCount int    `json:"memberCount"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// TeamMember 队伍成员
type TeamMember struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	IsCaptain   bool   `json:"isCaptain"`
	JoinedAt    string `json:"joinedAt"`
}

// CreateTeamRequest 创建队伍请求
type CreateTeamRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	CaptainID   int64  `json:"captainId"`
}

// UpdateTeamRequest 更新队伍请求
type UpdateTeamRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CaptainID   *int64 `json:"captainId"`
	Status      string `json:"status"`
}

// AddMemberRequest 添加成员请求
type AddMemberRequest struct {
	UserID int64 `json:"userId" binding:"required"`
}

// HandleListTeams 获取队伍列表
func HandleListTeams(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT t.id, t.name, COALESCE(t.description, ''), t.captain_id, 
		       COALESCE(u.display_name, '') as captain_name,
		       t.is_admin_team, t.status,
		       (SELECT COUNT(*) FROM users WHERE team_id = t.id) as member_count,
		       COALESCE(TO_CHAR(t.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at,
		       COALESCE(TO_CHAR(t.updated_at, 'YYYY-MM-DD HH24:MI'), '') as updated_at
		FROM teams t
		LEFT JOIN users u ON t.captain_id = u.id
		ORDER BY t.id ASC`)
	if err != nil {
		log.Printf("list teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var teams []TeamDetail
	for rows.Next() {
		var t TeamDetail
		var captainID sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &captainID,
			&t.CaptainName, &t.IsAdminTeam, &t.Status, &t.MemberCount,
			&t.CreatedAt, &t.UpdatedAt); err != nil {
			log.Printf("scan team error: %v", err)
			continue
		}
		if captainID.Valid {
			t.CaptainID = &captainID.Int64
		}
		teams = append(teams, t)
	}

	// 统计
	var total, activeCount, bannedCount int64
	db.QueryRow(`SELECT COUNT(*) FROM teams`).Scan(&total)
	db.QueryRow(`SELECT COUNT(*) FROM teams WHERE status = 'active'`).Scan(&activeCount)
	db.QueryRow(`SELECT COUNT(*) FROM teams WHERE status = 'banned'`).Scan(&bannedCount)

	c.JSON(http.StatusOK, gin.H{
		"teams": teams,
		"stats": gin.H{
			"total":       total,
			"activeCount": activeCount,
			"bannedCount": bannedCount,
		},
	})
}

// HandleCreateTeam 创建队伍
func HandleCreateTeam(c *gin.Context, db *sql.DB) {
	var req CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查队伍名是否已存在
	var exists int
	db.QueryRow(`SELECT 1 FROM teams WHERE name = $1`, req.Name).Scan(&exists)
	if exists == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "TEAM_NAME_EXISTS"})
		return
	}

	var captainID *int64
	if req.CaptainID > 0 {
		captainID = &req.CaptainID
	}

	var description *string
	if req.Description != "" {
		description = &req.Description
	}

	var id int64
	err := db.QueryRow(`INSERT INTO teams (name, description, captain_id, status, created_at, updated_at) 
		VALUES ($1, $2, $3, 'active', NOW(), NOW()) RETURNING id`,
		req.Name, description, captainID).Scan(&id)
	if err != nil {
		log.Printf("create team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 如果指定了队长，将队长加入队伍
	if captainID != nil {
		db.Exec(`UPDATE users SET team_id = $1 WHERE id = $2`, id, *captainID)
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "message": "CREATED"})
}

// HandleGetTeam 获取单个队伍
func HandleGetTeam(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var t TeamDetail
	var captainID sql.NullInt64
	err := db.QueryRow(`
		SELECT t.id, t.name, COALESCE(t.description, ''), t.captain_id, 
		       COALESCE(u.display_name, '') as captain_name,
		       t.is_admin_team, t.status,
		       (SELECT COUNT(*) FROM users WHERE team_id = t.id) as member_count,
		       COALESCE(TO_CHAR(t.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at,
		       COALESCE(TO_CHAR(t.updated_at, 'YYYY-MM-DD HH24:MI'), '') as updated_at
		FROM teams t
		LEFT JOIN users u ON t.captain_id = u.id
		WHERE t.id = $1`, id).Scan(
		&t.ID, &t.Name, &t.Description, &captainID,
		&t.CaptainName, &t.IsAdminTeam, &t.Status, &t.MemberCount,
		&t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("get team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	if captainID.Valid {
		t.CaptainID = &captainID.Int64
	}

	c.JSON(http.StatusOK, t)
}

// HandleUpdateTeam 更新队伍
func HandleUpdateTeam(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为管理员队伍
	var isAdminTeam bool
	db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, id).Scan(&isAdminTeam)
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_MODIFY_ADMIN_TEAM"})
		return
	}

	var req UpdateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var updates []string
	var args []interface{}
	argIndex := 1

	if req.Name != "" {
		// 检查新名称是否与其他队伍冲突
		var existsID int64
		err := db.QueryRow(`SELECT id FROM teams WHERE name = $1 AND id != $2`, req.Name, id).Scan(&existsID)
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
	if req.CaptainID != nil {
		updates = append(updates, "captain_id = $"+strconv.Itoa(argIndex))
		args = append(args, *req.CaptainID)
		argIndex++
	}
	if req.Status != "" {
		if req.Status != "active" && req.Status != "banned" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
			return
		}
		updates = append(updates, "status = $"+strconv.Itoa(argIndex))
		args = append(args, req.Status)
		argIndex++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, id)

	query := "UPDATE teams SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIndex)
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("update team error: %v", err)
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

// HandleDeleteTeam 删除队伍
func HandleDeleteTeam(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为管理员队伍
	var isAdminTeam bool
	db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, id).Scan(&isAdminTeam)
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_DELETE_ADMIN_TEAM"})
		return
	}

	// 先将队伍成员的 team_id 设为 NULL
	db.Exec(`UPDATE users SET team_id = NULL WHERE team_id = $1`, id)

	result, err := db.Exec(`DELETE FROM teams WHERE id = $1`, id)
	if err != nil {
		log.Printf("delete team error: %v", err)
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

// HandleGetTeamMembers 获取队伍成员
func HandleGetTeamMembers(c *gin.Context, db *sql.DB) {
	teamID := c.Param("id")

	// 先获取队长ID
	var captainID sql.NullInt64
	db.QueryRow(`SELECT captain_id FROM teams WHERE id = $1`, teamID).Scan(&captainID)

	rows, err := db.Query(`
		SELECT id, username, display_name, role,
		       COALESCE(TO_CHAR(created_at, 'YYYY-MM-DD HH24:MI'), '') as joined_at
		FROM users WHERE team_id = $1
		ORDER BY id ASC`, teamID)
	if err != nil {
		log.Printf("get team members error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.ID, &m.Username, &m.DisplayName, &m.Role, &m.JoinedAt); err != nil {
			log.Printf("scan member error: %v", err)
			continue
		}
		m.IsCaptain = captainID.Valid && m.ID == captainID.Int64
		members = append(members, m)
	}

	c.JSON(http.StatusOK, gin.H{"members": members})
}

// HandleAddTeamMember 添加队伍成员
func HandleAddTeamMember(c *gin.Context, db *sql.DB) {
	teamID := c.Param("id")

	// 检查是否为管理员队伍
	var isAdminTeam bool
	db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, teamID).Scan(&isAdminTeam)
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_MODIFY_ADMIN_TEAM"})
		return
	}

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查用户是否已在其他队伍
	var existingTeamID sql.NullInt64
	err := db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, req.UserID).Scan(&existingTeamID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if existingTeamID.Valid && strconv.FormatInt(existingTeamID.Int64, 10) != teamID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "USER_IN_OTHER_TEAM"})
		return
	}

	result, err := db.Exec(`UPDATE users SET team_id = $1 WHERE id = $2`, teamID, req.UserID)
	if err != nil {
		log.Printf("add team member error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "MEMBER_ADDED"})
}

// HandleRemoveTeamMember 移除队伍成员
func HandleRemoveTeamMember(c *gin.Context, db *sql.DB) {
	teamID := c.Param("id")
	userID := c.Param("userId")

	// 检查是否为管理员队伍
	var isAdminTeam bool
	db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, teamID).Scan(&isAdminTeam)
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_MODIFY_ADMIN_TEAM"})
		return
	}

	// 检查是否为队长
	var captainID sql.NullInt64
	db.QueryRow(`SELECT captain_id FROM teams WHERE id = $1`, teamID).Scan(&captainID)
	userIDInt, _ := strconv.ParseInt(userID, 10, 64)
	if captainID.Valid && userIDInt == captainID.Int64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CANNOT_REMOVE_CAPTAIN"})
		return
	}

	result, err := db.Exec(`UPDATE users SET team_id = NULL WHERE id = $1 AND team_id = $2`, userID, teamID)
	if err != nil {
		log.Printf("remove team member error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "MEMBER_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "MEMBER_REMOVED"})
}

// HandleSetTeamCaptain 设置队长
func HandleSetTeamCaptain(c *gin.Context, db *sql.DB) {
	teamID := c.Param("id")
	userID := c.Param("userId")

	// 检查是否为管理员队伍
	var isAdminTeam bool
	db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, teamID).Scan(&isAdminTeam)
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "CANNOT_MODIFY_ADMIN_TEAM"})
		return
	}

	// 检查用户是否在该队伍
	var currentTeamID sql.NullInt64
	err := db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&currentTeamID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if !currentTeamID.Valid || strconv.FormatInt(currentTeamID.Int64, 10) != teamID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "USER_NOT_IN_TEAM"})
		return
	}

	result, err := db.Exec(`UPDATE teams SET captain_id = $1, updated_at = NOW() WHERE id = $2`, userID, teamID)
	if err != nil {
		log.Printf("set team captain error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "TEAM_NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "CAPTAIN_SET"})
}

// HandleGetUsersWithoutTeam 获取未加入队伍的用户列表
func HandleGetUsersWithoutTeam(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, username, display_name, role
		FROM users 
		WHERE team_id IS NULL AND role != 'super'
		ORDER BY id ASC`)
	if err != nil {
		log.Printf("get users without team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	type SimpleUser struct {
		ID          int64  `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Role        string `json:"role"`
	}

	var users []SimpleUser
	for rows.Next() {
		var u SimpleUser
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role); err != nil {
			continue
		}
		users = append(users, u)
	}

	c.JSON(http.StatusOK, gin.H{"users": users})
}
