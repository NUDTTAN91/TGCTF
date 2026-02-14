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

	"tgctf/server/contest"

	"github.com/gin-gonic/gin"
)

// OrganizationDetail 组织详情
type OrganizationDetail struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	UserCount   int    `json:"userCount"`
	TeamCount   int    `json:"teamCount"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// HandleListOrganizations 获取组织列表
func HandleListOrganizations(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT o.id, o.name, COALESCE(o.description, ''), o.status,
		       (SELECT COUNT(*) FROM users WHERE organization_id = o.id) as user_count,
		       (SELECT COUNT(*) FROM teams WHERE organization_id = o.id) as team_count,
		       COALESCE(TO_CHAR(o.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at,
		       COALESCE(TO_CHAR(o.updated_at, 'YYYY-MM-DD HH24:MI'), '') as updated_at
		FROM organizations o
		ORDER BY o.id ASC`)
	if err != nil {
		log.Printf("list organizations error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var orgs []OrganizationDetail
	for rows.Next() {
		var o OrganizationDetail
		if err := rows.Scan(&o.ID, &o.Name, &o.Description, &o.Status, &o.UserCount, &o.TeamCount, &o.CreatedAt, &o.UpdatedAt); err != nil {
			log.Printf("scan organization error: %v", err)
			continue
		}
		orgs = append(orgs, o)
	}

	if orgs == nil {
		orgs = []OrganizationDetail{}
	}
	c.JSON(http.StatusOK, orgs)
}

// HandleCreateOrganization 创建组织
func HandleCreateOrganization(c *gin.Context, db *sql.DB) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NAME_REQUIRED", "message": "组织名称不能为空"})
		return
	}

	var id int64
	err := db.QueryRow(`INSERT INTO organizations (name, description, status, created_at, updated_at) 
		VALUES ($1, $2, 'active', NOW(), NOW()) RETURNING id`,
		req.Name, req.Description).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			c.JSON(http.StatusConflict, gin.H{"error": "NAME_EXISTS", "message": "组织名称已存在"})
			return
		}
		log.Printf("create organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "message": "创建成功"})
}

// HandleGetOrganization 获取组织详情
func HandleGetOrganization(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	var o OrganizationDetail
	err := db.QueryRow(`
		SELECT o.id, o.name, COALESCE(o.description, ''), o.status,
		       (SELECT COUNT(*) FROM users WHERE organization_id = o.id) as user_count,
		       (SELECT COUNT(*) FROM teams WHERE organization_id = o.id) as team_count,
		       COALESCE(TO_CHAR(o.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at,
		       COALESCE(TO_CHAR(o.updated_at, 'YYYY-MM-DD HH24:MI'), '') as updated_at
		FROM organizations o WHERE o.id = $1`, orgID).Scan(
		&o.ID, &o.Name, &o.Description, &o.Status, &o.UserCount, &o.TeamCount, &o.CreatedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("get organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, o)
}

// HandleUpdateOrganization 更新组织
func HandleUpdateOrganization(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	// 检查是否为 TG_AdminX 组织
	var orgName string
	db.QueryRow(`SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName)
	if orgName == "TG_AdminX" {
		c.JSON(http.StatusForbidden, gin.H{"error": "PROTECTED_ORG", "message": "TG_AdminX组织不允许修改"})
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NAME_REQUIRED", "message": "组织名称不能为空"})
		return
	}

	// 验证状态
	if req.Status != "" && req.Status != "active" && req.Status != "disabled" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS", "message": "无效的状态"})
		return
	}

	result, err := db.Exec(`UPDATE organizations SET name = $1, description = $2, status = COALESCE(NULLIF($3, ''), status), updated_at = NOW() WHERE id = $4`,
		req.Name, req.Description, req.Status, orgID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			c.JSON(http.StatusConflict, gin.H{"error": "NAME_EXISTS", "message": "组织名称已存在"})
			return
		}
		log.Printf("update organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}

// HandleDeleteOrganization 删除组织（级联删除组织内的所有用户和队伍）
func HandleDeleteOrganization(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	// 获取请求参数：是否删除用户担任队长的外部队伍
	var req struct {
		DeleteCaptainTeams bool `json:"deleteCaptainTeams"`
	}
	c.ShouldBindJSON(&req)

	// 检查是否为 TG_AdminX 组织
	var orgName string
	db.QueryRow(`SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName)
	if orgName == "TG_AdminX" {
		c.JSON(http.StatusForbidden, gin.H{"error": "PROTECTED_ORG", "message": "TG_AdminX组织不允许删除"})
		return
	}

	// 开始事务
	tx, err := db.Begin()
	if err != nil {
		log.Printf("begin transaction error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer tx.Rollback()

	// 1. 获取组织下的所有用户ID
	var userIDs []int64
	userRows, err := tx.Query(`SELECT id FROM users WHERE organization_id = $1`, orgID)
	if err != nil {
		log.Printf("query users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	for userRows.Next() {
		var userID int64
		if err := userRows.Scan(&userID); err == nil {
			userIDs = append(userIDs, userID)
		}
	}
	userRows.Close()

	// 2. 获取组织下的所有队伍ID
	var teamIDs []int64
	teamRows, err := tx.Query(`SELECT id FROM teams WHERE organization_id = $1`, orgID)
	if err != nil {
		log.Printf("query teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	for teamRows.Next() {
		var teamID int64
		if err := teamRows.Scan(&teamID); err == nil {
			teamIDs = append(teamIDs, teamID)
		}
	}
	teamRows.Close()

	// 3. 查找组织内用户担任队长的外部队伍（队伍不属于该组织）
	var captainTeamIDs []int64
	for _, userID := range userIDs {
		captainRows, err := tx.Query(`SELECT id FROM teams WHERE captain_id = $1 AND (organization_id IS NULL OR organization_id != $2)`, userID, orgID)
		if err != nil {
			log.Printf("query captain teams error: %v", err)
			continue
		}
		for captainRows.Next() {
			var teamID int64
			if err := captainRows.Scan(&teamID); err == nil {
				captainTeamIDs = append(captainTeamIDs, teamID)
			}
		}
		captainRows.Close()
	}

	// 4. 处理用户担任队长的外部队伍
	var deletedCaptainTeams int64 = 0
	for _, teamID := range captainTeamIDs {
		if req.DeleteCaptainTeams {
			// 删除队伍相关数据（所有引用该队伍的表）
			tx.Exec(`DELETE FROM challenge_first_views WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM submissions WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM system_logs WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM team_challenge_flags WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM team_instances WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM team_solves WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM contest_teams WHERE team_id = $1`, teamID)
			tx.Exec(`UPDATE users SET team_id = NULL WHERE team_id = $1`, teamID)
			tx.Exec(`DELETE FROM teams WHERE id = $1`, teamID)
			deletedCaptainTeams++
		} else {
			// 随机选择新队长
			var newCaptainID sql.NullInt64
			err := tx.QueryRow(`SELECT id FROM users WHERE team_id = $1 AND id NOT IN (SELECT captain_id FROM teams WHERE id = $1 AND captain_id IS NOT NULL) LIMIT 1`, teamID).Scan(&newCaptainID)
			if err == nil && newCaptainID.Valid {
				tx.Exec(`UPDATE teams SET captain_id = $1, updated_at = NOW() WHERE id = $2`, newCaptainID.Int64, teamID)
			} else {
				tx.Exec(`UPDATE teams SET captain_id = NULL, updated_at = NOW() WHERE id = $1`, teamID)
			}
		}
	}

	// 5. 删除组织内队伍相关的所有引用数据
	for _, teamID := range teamIDs {
		tx.Exec(`DELETE FROM challenge_first_views WHERE team_id = $1`, teamID)
		tx.Exec(`DELETE FROM submissions WHERE team_id = $1`, teamID)
		tx.Exec(`DELETE FROM system_logs WHERE team_id = $1`, teamID)
		tx.Exec(`DELETE FROM team_challenge_flags WHERE team_id = $1`, teamID)
		tx.Exec(`DELETE FROM team_instances WHERE team_id = $1`, teamID)
		tx.Exec(`DELETE FROM team_solves WHERE team_id = $1`, teamID)
		tx.Exec(`DELETE FROM contest_teams WHERE team_id = $1`, teamID)
	}

	// 6. 删除用户相关的所有引用数据
	for _, userID := range userIDs {
		tx.Exec(`DELETE FROM challenge_first_views WHERE user_id = $1`, userID)
		tx.Exec(`DELETE FROM submissions WHERE user_id = $1`, userID)
		tx.Exec(`DELETE FROM system_logs WHERE user_id = $1`, userID)
		tx.Exec(`DELETE FROM team_instances WHERE created_by = $1`, userID)
		tx.Exec(`UPDATE team_solves SET first_solver_id = NULL WHERE first_solver_id = $1`, userID)
		tx.Exec(`UPDATE contest_teams SET reviewed_by = NULL WHERE reviewed_by = $1`, userID)
		tx.Exec(`UPDATE contest_announcements SET created_by = NULL WHERE created_by = $1`, userID)
	}

	// 7. 删除组织关联的比赛记录
	tx.Exec(`DELETE FROM contest_organizations WHERE organization_id = $1`, orgID)

	// 8. 清除用户的队伍引用（因为队伍要被删除）
	for _, teamID := range teamIDs {
		tx.Exec(`UPDATE users SET team_id = NULL WHERE team_id = $1`, teamID)
	}

	// 9. 删除组织下的所有用户
	userResult, err := tx.Exec(`DELETE FROM users WHERE organization_id = $1`, orgID)
	if err != nil {
		log.Printf("delete organization users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "删除用户失败: " + err.Error()})
		return
	}
	deletedUsers, _ := userResult.RowsAffected()

	// 10. 删除组织下的所有队伍
	teamResult, err := tx.Exec(`DELETE FROM teams WHERE organization_id = $1`, orgID)
	if err != nil {
		log.Printf("delete organization teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "删除队伍失败: " + err.Error()})
		return
	}
	deletedTeams, _ := teamResult.RowsAffected()

	// 11. 删除组织本身
	result, err := tx.Exec(`DELETE FROM organizations WHERE id = $1`, orgID)
	if err != nil {
		log.Printf("delete organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		log.Printf("commit transaction error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	log.Printf("Deleted organization %s (ID: %s), %d users, %d teams, %d captain teams", orgName, orgID, deletedUsers, deletedTeams, deletedCaptainTeams)
	c.JSON(http.StatusOK, gin.H{
		"message":             "删除成功",
		"deletedUsers":        deletedUsers,
		"deletedTeams":        deletedTeams,
		"deletedCaptainTeams": deletedCaptainTeams,
	})
}

// HandleCheckOrganizationCaptains 检查组织内用户担任队长的外部队伍
func HandleCheckOrganizationCaptains(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	// 查找组织内用户担任队长的外部队伍
	rows, err := db.Query(`
		SELECT t.id, t.name, u.display_name as captain_name
		FROM teams t
		JOIN users u ON t.captain_id = u.id
		WHERE u.organization_id = $1 AND (t.organization_id IS NULL OR t.organization_id != $1)`, orgID)
	if err != nil {
		log.Printf("check organization captains error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var captainTeams []gin.H
	for rows.Next() {
		var teamID int64
		var teamName, captainName string
		if err := rows.Scan(&teamID, &teamName, &captainName); err != nil {
			continue
		}
		captainTeams = append(captainTeams, gin.H{
			"teamId":      teamID,
			"teamName":    teamName,
			"captainName": captainName,
		})
	}

	if captainTeams == nil {
		captainTeams = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"captainTeams": captainTeams})
}

// HandleGetOrganizationUsers 获取组织下的用户
func HandleGetOrganizationUsers(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	rows, err := db.Query(`
		SELECT u.id, u.username, u.display_name, u.role, COALESCE(u.email, ''),
		       COALESCE(t.name, '') as team_name,
		       COALESCE(TO_CHAR(u.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at
		FROM users u
		LEFT JOIN teams t ON u.team_id = t.id
		WHERE u.organization_id = $1
		ORDER BY u.id ASC`, orgID)
	if err != nil {
		log.Printf("get organization users error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id int64
		var username, displayName, role, email, teamName, createdAt string
		if err := rows.Scan(&id, &username, &displayName, &role, &email, &teamName, &createdAt); err != nil {
			continue
		}
		users = append(users, gin.H{
			"id":          id,
			"username":    username,
			"displayName": displayName,
			"role":        role,
			"email":       email,
			"teamName":    teamName,
			"createdAt":   createdAt,
		})
	}

	if users == nil {
		users = []gin.H{}
	}
	c.JSON(http.StatusOK, users)
}

// HandleGetOrganizationTeams 获取组织下的队伍
func HandleGetOrganizationTeams(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	rows, err := db.Query(`
		SELECT t.id, t.name, COALESCE(t.description, ''), t.status,
		       (SELECT COUNT(*) FROM users WHERE team_id = t.id) as member_count,
		       COALESCE(u.display_name, '') as captain_name,
		       COALESCE(TO_CHAR(t.created_at, 'YYYY-MM-DD HH24:MI'), '') as created_at
		FROM teams t
		LEFT JOIN users u ON t.captain_id = u.id
		WHERE t.organization_id = $1
		ORDER BY t.id ASC`, orgID)
	if err != nil {
		log.Printf("get organization teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var teams []gin.H
	for rows.Next() {
		var id int64
		var name, description, status, captainName, createdAt string
		var memberCount int
		if err := rows.Scan(&id, &name, &description, &status, &memberCount, &captainName, &createdAt); err != nil {
			continue
		}
		teams = append(teams, gin.H{
			"id":          id,
			"name":        name,
			"description": description,
			"status":      status,
			"memberCount": memberCount,
			"captainName": captainName,
			"createdAt":   createdAt,
		})
	}

	if teams == nil {
		teams = []gin.H{}
	}
	c.JSON(http.StatusOK, teams)
}

// HandleAddUserToOrganization 将用户添加到组织
func HandleAddUserToOrganization(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	// 检查是否为 TG_AdminX 组织
	var orgName string
	db.QueryRow(`SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName)
	if orgName == "TG_AdminX" {
		c.JSON(http.StatusForbidden, gin.H{"error": "PROTECTED_ORG", "message": "TG_AdminX组织成员由系统自动管理"})
		return
	}

	var req struct {
		UserID int64 `json:"userId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查用户是否存在且不是超管
	var role string
	err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, req.UserID).Scan(&role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if role == "super" {
		c.JSON(http.StatusForbidden, gin.H{"error": "SUPER_ADMIN_NO_ORG", "message": "超级管理员不能加入组织"})
		return
	}

	// 更新用户的组织
	_, err = db.Exec(`UPDATE users SET organization_id = $1, updated_at = NOW() WHERE id = $2`, orgID, req.UserID)
	if err != nil {
		log.Printf("add user to organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "添加成功"})
}

// HandleRemoveUserFromOrganization 将用户从组织移除
func HandleRemoveUserFromOrganization(c *gin.Context, db *sql.DB) {
	userID := c.Param("userId")

	// 检查用户是否存在
	var currentOrgID sql.NullInt64
	err := db.QueryRow(`SELECT organization_id FROM users WHERE id = $1`, userID).Scan(&currentOrgID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}

	// 清除用户的组织和队伍
	_, err = db.Exec(`UPDATE users SET organization_id = NULL, team_id = NULL, updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		log.Printf("remove user from organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "移除成功"})
}

// HandleAddTeamToOrganization 将队伍添加到组织
func HandleAddTeamToOrganization(c *gin.Context, db *sql.DB) {
	orgID := c.Param("id")

	// 检查是否为 TG_AdminX 组织
	var orgName string
	db.QueryRow(`SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName)
	if orgName == "TG_AdminX" {
		c.JSON(http.StatusForbidden, gin.H{"error": "PROTECTED_ORG", "message": "TG_AdminX组织成员由系统自动管理"})
		return
	}

	var req struct {
		TeamID int64 `json:"teamId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查队伍是否存在且不是管理员队伍
	var isAdminTeam bool
	err := db.QueryRow(`SELECT is_admin_team FROM teams WHERE id = $1`, req.TeamID).Scan(&isAdminTeam)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "TEAM_NOT_FOUND"})
		return
	}
	if isAdminTeam {
		c.JSON(http.StatusForbidden, gin.H{"error": "ADMIN_TEAM_NO_ORG", "message": "管理员队伍不能加入组织"})
		return
	}

	orgIDInt, _ := strconv.ParseInt(orgID, 10, 64)

	// 更新队伍的组织
	_, err = db.Exec(`UPDATE teams SET organization_id = $1, updated_at = NOW() WHERE id = $2`, orgIDInt, req.TeamID)
	if err != nil {
		log.Printf("add team to organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 同时更新队伍成员的组织
	_, err = db.Exec(`UPDATE users SET organization_id = $1, updated_at = NOW() WHERE team_id = $2`, orgIDInt, req.TeamID)
	if err != nil {
		log.Printf("update team members organization error: %v", err)
	}

	// 查找该组织关联的所有比赛，将队伍自动加入并通过审核
	contestRows, err := db.Query(`SELECT contest_id FROM contest_organizations WHERE organization_id = $1`, orgIDInt)
	if err != nil {
		log.Printf("query organization contests error: %v", err)
	} else {
		defer contestRows.Close()
		var contestIDs []string
		for contestRows.Next() {
			var contestID int64
			if err := contestRows.Scan(&contestID); err == nil {
				contestIDs = append(contestIDs, strconv.FormatInt(contestID, 10))
			}
		}

		// 将队伍加入每个比赛并自动通过审核
		for _, contestID := range contestIDs {
			_, err = db.Exec(`INSERT INTO contest_teams (contest_id, team_id, status, reviewed_at, created_at, updated_at) 
				VALUES ($1, $2, 'approved', NOW(), NOW(), NOW()) 
				ON CONFLICT (contest_id, team_id) DO NOTHING`, contestID, req.TeamID)
			if err != nil {
				log.Printf("insert contest team error: %v", err)
				continue
			}

			// 为该队伍生成该比赛的Flag
			if contest.GenerateFlagsForTeamInContest != nil {
				go contest.GenerateFlagsForTeamInContest(db, contestID, req.TeamID, "")
			}

			// 广播队伍列表更新
			go contest.BroadcastFullRefresh(contestID)
		}

		if len(contestIDs) > 0 {
			log.Printf("Team %d auto joined %d contests via organization %s", req.TeamID, len(contestIDs), orgID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "添加成功"})
}

// HandleRemoveTeamFromOrganization 将队伍从组织移除
func HandleRemoveTeamFromOrganization(c *gin.Context, db *sql.DB) {
	teamID := c.Param("teamId")

	// 清除队伍的组织
	_, err := db.Exec(`UPDATE teams SET organization_id = NULL, updated_at = NOW() WHERE id = $1`, teamID)
	if err != nil {
		log.Printf("remove team from organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 同时清除队伍成员的组织
	_, err = db.Exec(`UPDATE users SET organization_id = NULL, updated_at = NOW() WHERE team_id = $1`, teamID)
	if err != nil {
		log.Printf("clear team members organization error: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{"message": "移除成功"})
}

// HandleGetUsersWithoutOrganization 获取未加入组织的用户（排除超管）
func HandleGetUsersWithoutOrganization(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, username, display_name, role
		FROM users 
		WHERE organization_id IS NULL AND role != 'super'
		ORDER BY id ASC`)
	if err != nil {
		log.Printf("get users without organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id int64
		var username, displayName, role string
		if err := rows.Scan(&id, &username, &displayName, &role); err != nil {
			continue
		}
		users = append(users, gin.H{
			"id":          id,
			"username":    username,
			"displayName": displayName,
			"role":        role,
		})
	}

	if users == nil {
		users = []gin.H{}
	}
	c.JSON(http.StatusOK, users)
}

// HandleGetTeamsWithoutOrganization 获取未加入组织的队伍（排除管理员队伍）
func HandleGetTeamsWithoutOrganization(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, name, COALESCE(description, '')
		FROM teams 
		WHERE organization_id IS NULL AND is_admin_team = FALSE
		ORDER BY id ASC`)
	if err != nil {
		log.Printf("get teams without organization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var teams []gin.H
	for rows.Next() {
		var id int64
		var name, description string
		if err := rows.Scan(&id, &name, &description); err != nil {
			continue
		}
		teams = append(teams, gin.H{
			"id":          id,
			"name":        name,
			"description": description,
		})
	}

	if teams == nil {
		teams = []gin.H{}
	}
	c.JSON(http.StatusOK, teams)
}

// HandleGetContestOrganizations 获取比赛关联的组织
func HandleGetContestOrganizations(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	rows, err := db.Query(`
		SELECT o.id, o.name
		FROM organizations o
		JOIN contest_organizations co ON o.id = co.organization_id
		WHERE co.contest_id = $1
		ORDER BY o.name ASC`, contestID)
	if err != nil {
		log.Printf("get contest organizations error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var orgs []gin.H
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		orgs = append(orgs, gin.H{
			"id":   id,
			"name": name,
		})
	}

	if orgs == nil {
		orgs = []gin.H{}
	}
	c.JSON(http.StatusOK, orgs)
}

// HandleSetContestOrganizations 设置比赛关联的组织（多选）
func HandleSetContestOrganizations(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	var req struct {
		OrganizationIDs []int64 `json:"organizationIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 开始事务
	tx, err := db.Begin()
	if err != nil {
		log.Printf("begin transaction error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer tx.Rollback()

	// 删除旧的组织关联
	_, err = tx.Exec(`DELETE FROM contest_organizations WHERE contest_id = $1`, contestID)
	if err != nil {
		log.Printf("delete old contest organizations error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 先删除该比赛的所有队伍 Flag
	_, err = tx.Exec(`DELETE FROM team_challenge_flags WHERE contest_id = $1`, contestID)
	if err != nil {
		log.Printf("delete all contest flags error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 无条件删除该比赛的所有队伍审核记录
	_, err = tx.Exec(`DELETE FROM contest_teams WHERE contest_id = $1`, contestID)
	if err != nil {
		log.Printf("delete all contest teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 如果不选择任何组织，比赛为公开比赛
	if len(req.OrganizationIDs) == 0 {
		if err := tx.Commit(); err != nil {
			log.Printf("commit transaction error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "已设置为公开比赛，队伍可自行报名"})
		return
	}

	// 收集所有需要生成Flag的队伍ID
	var allApprovedTeamIDs []int64

	// 添加新的组织关联，并自动将组织下的队伍加入比赛（自动通过审核）
	for _, orgID := range req.OrganizationIDs {
		_, err = tx.Exec(`INSERT INTO contest_organizations (contest_id, organization_id, created_at) VALUES ($1, $2, NOW())`,
			contestID, orgID)
		if err != nil {
			log.Printf("insert contest organization error: %v", err)
			continue
		}

		// 自动将该组织下的队伍加入比赛
		rows, err := tx.Query(`SELECT id FROM teams WHERE organization_id = $1 AND is_admin_team = FALSE AND status = 'active'`, orgID)
		if err != nil {
			log.Printf("query organization teams error: %v", err)
			continue
		}

		var teamIDs []int64
		for rows.Next() {
			var teamID int64
			if err := rows.Scan(&teamID); err == nil {
				teamIDs = append(teamIDs, teamID)
			}
		}
		rows.Close()

		// 将队伍加入比赛（自动通过审核）
		for _, teamID := range teamIDs {
			_, err = tx.Exec(`INSERT INTO contest_teams (contest_id, team_id, status, reviewed_at, created_at, updated_at) 
				VALUES ($1, $2, 'approved', NOW(), NOW(), NOW()) 
				ON CONFLICT (contest_id, team_id) DO NOTHING`, contestID, teamID)
			if err != nil {
				log.Printf("insert contest team error: %v", err)
			} else {
				allApprovedTeamIDs = append(allApprovedTeamIDs, teamID)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("commit transaction error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 为所有自动通过审核的队伍生成Flag
	if len(allApprovedTeamIDs) > 0 && contest.GenerateFlagsForTeamInContest != nil {
		go func() {
			for _, teamID := range allApprovedTeamIDs {
				contest.GenerateFlagsForTeamInContest(db, contestID, teamID, "")
			}
			log.Printf("Generated flags for %d teams in contest %s", len(allApprovedTeamIDs), contestID)
		}()
	}

	// 广播队伍列表更新
	go func() {
		// 组织变更后，队伍列表大幅度变化，广播完全刷新信号
		contest.BroadcastFullRefresh(contestID)
	}()

	c.JSON(http.StatusOK, gin.H{"message": "设置成功，队伍已自动通过审核"})
}
