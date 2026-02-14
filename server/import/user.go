// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package dataimport

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"tgctf/server/contest"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)

// ImportUserRow 导入用户数据行
type ImportUserRow struct {
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	Email        string `json:"email"`
	TeamName     string `json:"teamName"`
	Organization string `json:"organization"`
}

// ImportResult 导入结果
type ImportResult struct {
	Total        int      `json:"total"`
	Success      int      `json:"success"`
	Failed       int      `json:"failed"`
	Errors       []string `json:"errors"`
	CreatedOrgs  []string `json:"createdOrgs"`
	CreatedTeams []string `json:"createdTeams"`
}

// HandleImportUsers 导入用户（JSON格式）
func HandleImportUsers(c *gin.Context, db *sql.DB) {
	var req struct {
		Users []ImportUserRow `json:"users"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "请求格式错误"})
		return
	}

	result := processUserImport(db, req.Users)
	c.JSON(http.StatusOK, result)
}

// HandleImportUsersExcel 通过Excel导入用户
func HandleImportUsersExcel(c *gin.Context, db *sql.DB) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_REQUIRED", "message": "请上传Excel文件"})
		return
	}
	defer file.Close()

	// 读取Excel文件
	f, err := excelize.OpenReader(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_FILE", "message": "无法读取Excel文件: " + err.Error()})
		return
	}
	defer f.Close()

	// 获取第一个工作表
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "EMPTY_FILE", "message": "Excel文件为空"})
		return
	}

	rows, err := f.GetRows(sheets[0])
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "READ_ERROR", "message": "读取工作表失败"})
		return
	}

	if len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_DATA", "message": "Excel文件没有数据（需要表头+至少一行数据）"})
		return
	}

	// 解析表头，确定列映射
	header := rows[0]
	colMap := make(map[string]int)
	for i, col := range header {
		col = strings.TrimSpace(strings.ToLower(col))
		switch {
		case col == "用户名" || col == "username":
			colMap["username"] = i
		case col == "真实姓名" || col == "姓名" || col == "displayname" || col == "display_name" || col == "name":
			colMap["displayName"] = i
		case col == "邮箱" || col == "email":
			colMap["email"] = i
		case col == "队伍" || col == "所属队伍" || col == "team" || col == "teamname" || col == "team_name":
			colMap["teamName"] = i
		case col == "组织" || col == "机构" || col == "所属组织" || col == "所属机构" || col == "organization" || col == "org":
			colMap["organization"] = i
		}
	}

	// 检查必须的列
	if _, ok := colMap["username"]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MISSING_COLUMN", "message": "缺少必须的列：用户名"})
		return
	}
	if _, ok := colMap["displayName"]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MISSING_COLUMN", "message": "缺少必须的列：真实姓名"})
		return
	}

	// 解析数据行
	var users []ImportUserRow
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if len(row) == 0 {
			continue
		}

		user := ImportUserRow{}
		if idx, ok := colMap["username"]; ok && idx < len(row) {
			user.Username = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["displayName"]; ok && idx < len(row) {
			user.DisplayName = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["email"]; ok && idx < len(row) {
			user.Email = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["teamName"]; ok && idx < len(row) {
			user.TeamName = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["organization"]; ok && idx < len(row) {
			user.Organization = strings.TrimSpace(row[idx])
		}

		// 跳过用户名为空的行
		if user.Username == "" {
			continue
		}

		users = append(users, user)
	}

	if len(users) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_VALID_DATA", "message": "没有有效的用户数据"})
		return
	}

	result := processUserImport(db, users)
	c.JSON(http.StatusOK, result)
}

// processUserImport 处理用户导入逻辑
func processUserImport(db *sql.DB, users []ImportUserRow) ImportResult {
	result := ImportResult{
		Total:        len(users),
		Errors:       []string{},
		CreatedOrgs:  []string{},
		CreatedTeams: []string{},
	}

	// 默认密码 123456
	defaultPassword := "123456"
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("hash password error: %v", err)
		result.Errors = append(result.Errors, "密码加密失败")
		return result
	}

	// 缓存已创建的组织和队伍ID
	orgCache := make(map[string]int64)
	teamCache := make(map[string]int64)

	for i, user := range users {
		rowNum := i + 2 // Excel行号（从1开始，加上表头）

		// 验证必填字段
		if user.Username == "" {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 用户名不能为空", rowNum))
			continue
		}
		if user.DisplayName == "" {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 真实姓名不能为空", rowNum))
			continue
		}

		// 检查用户名是否已存在
		var existingID int64
		err := db.QueryRow(`SELECT id FROM users WHERE username = $1`, user.Username).Scan(&existingID)
		if err == nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 用户名 '%s' 已存在", rowNum, user.Username))
			continue
		}

		// 获取或创建组织
		var orgID *int64
		if user.Organization != "" {
			id, isNew, err := getOrCreateOrganization(db, user.Organization, orgCache)
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 创建组织失败 - %v", rowNum, err))
				continue
			}
			orgID = &id
			if isNew {
				result.CreatedOrgs = append(result.CreatedOrgs, user.Organization)
			}
		}

		// 获取或创建队伍（队伍需要关联组织）
		var teamID *int64
		if user.TeamName != "" {
			id, isNew, err := getOrCreateTeam(db, user.TeamName, orgID, teamCache)
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 创建队伍失败 - %v", rowNum, err))
				continue
			}
			teamID = &id
			if isNew {
				result.CreatedTeams = append(result.CreatedTeams, user.TeamName)
			}
		}

		// 创建用户
		var email *string
		if user.Email != "" {
			email = &user.Email
		}

		_, err = db.Exec(`
			INSERT INTO users (username, display_name, email, password_hash, role, status, team_id, organization_id, must_change_password, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'user', 'active', $5, $6, TRUE, NOW(), NOW())`,
			user.Username, user.DisplayName, email, string(hashedPassword), teamID, orgID)
		if err != nil {
			result.Failed++
			if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 用户名 '%s' 已存在", rowNum, user.Username))
			} else {
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行: 创建用户失败 - %v", rowNum, err))
			}
			continue
		}

		result.Success++
	}

	// 去重
	result.CreatedOrgs = uniqueStrings(result.CreatedOrgs)
	result.CreatedTeams = uniqueStrings(result.CreatedTeams)

	// 为没有队长的队伍随机抽取一名队员作为队长
	assignRandomCaptains(db)

	return result
}

// assignRandomCaptains 为没有队长的队伍随机抽取一名队员作为队长
func assignRandomCaptains(db *sql.DB) {
	rows, err := db.Query(`
		SELECT t.id FROM teams t
		WHERE t.captain_id IS NULL 
		AND t.is_admin_team = FALSE
		AND EXISTS (SELECT 1 FROM users u WHERE u.team_id = t.id)`)
	if err != nil {
		log.Printf("query teams without captain error: %v", err)
		return
	}
	defer rows.Close()

	var teamIDs []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			teamIDs = append(teamIDs, id)
		}
	}

	for _, teamID := range teamIDs {
		var userID int64
		err := db.QueryRow(`SELECT id FROM users WHERE team_id = $1 ORDER BY RANDOM() LIMIT 1`, teamID).Scan(&userID)
		if err != nil {
			continue
		}

		_, err = db.Exec(`UPDATE teams SET captain_id = $1, updated_at = NOW() WHERE id = $2`, userID, teamID)
		if err != nil {
			log.Printf("assign captain for team %d error: %v", teamID, err)
		}
	}
}

// getOrCreateOrganization 获取或创建组织
func getOrCreateOrganization(db *sql.DB, name string, cache map[string]int64) (int64, bool, error) {
	if id, ok := cache[name]; ok {
		return id, false, nil
	}

	var id int64
	err := db.QueryRow(`SELECT id FROM organizations WHERE name = $1`, name).Scan(&id)
	if err == nil {
		cache[name] = id
		return id, false, nil
	}

	if err != sql.ErrNoRows {
		return 0, false, err
	}

	err = db.QueryRow(`
		INSERT INTO organizations (name, status, created_at, updated_at) 
		VALUES ($1, 'active', NOW(), NOW()) 
		RETURNING id`, name).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			err = db.QueryRow(`SELECT id FROM organizations WHERE name = $1`, name).Scan(&id)
			if err == nil {
				cache[name] = id
				return id, false, nil
			}
		}
		return 0, false, err
	}

	cache[name] = id
	return id, true, nil
}

// getOrCreateTeam 获取或创建队伍
func getOrCreateTeam(db *sql.DB, name string, orgID *int64, cache map[string]int64) (int64, bool, error) {
	cacheKey := name
	if orgID != nil {
		cacheKey = fmt.Sprintf("%s_%d", name, *orgID)
	}

	if id, ok := cache[cacheKey]; ok {
		return id, false, nil
	}

	var id int64
	var query string
	var args []interface{}
	if orgID != nil {
		query = `SELECT id FROM teams WHERE name = $1 AND (organization_id = $2 OR organization_id IS NULL)`
		args = []interface{}{name, *orgID}
	} else {
		query = `SELECT id FROM teams WHERE name = $1 AND organization_id IS NULL`
		args = []interface{}{name}
	}

	err := db.QueryRow(query, args...).Scan(&id)
	if err == nil {
		if orgID != nil {
			db.Exec(`UPDATE teams SET organization_id = $1, updated_at = NOW() WHERE id = $2 AND organization_id IS NULL`, *orgID, id)
		}
		cache[cacheKey] = id
		return id, false, nil
	}

	if err != sql.ErrNoRows {
		return 0, false, err
	}

	err = db.QueryRow(`
		INSERT INTO teams (name, organization_id, is_admin_team, status, created_at, updated_at) 
		VALUES ($1, $2, FALSE, 'active', NOW(), NOW()) 
		RETURNING id`, name, orgID).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			err = db.QueryRow(`SELECT id FROM teams WHERE name = $1`, name).Scan(&id)
			if err == nil {
				cache[cacheKey] = id
				return id, false, nil
			}
		}
		return 0, false, err
	}

	// 新创建的队伍，自动加入该组织关联的所有比赛
	if orgID != nil {
		autoJoinContestsForTeam(db, id, *orgID)
	}

	cache[cacheKey] = id
	return id, true, nil
}

// autoJoinContestsForTeam 将队伍自动加入其组织关联的所有比赛
func autoJoinContestsForTeam(db *sql.DB, teamID int64, orgID int64) {
	// 查找该组织关联的所有比赛
	rows, err := db.Query(`SELECT contest_id FROM contest_organizations WHERE organization_id = $1`, orgID)
	if err != nil {
		log.Printf("query organization contests for auto join error: %v", err)
		return
	}
	defer rows.Close()

	var contestIDs []string
	for rows.Next() {
		var contestID int64
		if err := rows.Scan(&contestID); err == nil {
			contestIDs = append(contestIDs, strconv.FormatInt(contestID, 10))
		}
	}

	// 将队伍加入每个比赛并自动通过审核
	for _, contestID := range contestIDs {
		_, err = db.Exec(`INSERT INTO contest_teams (contest_id, team_id, status, reviewed_at, created_at, updated_at) 
			VALUES ($1, $2, 'approved', NOW(), NOW(), NOW()) 
			ON CONFLICT (contest_id, team_id) DO NOTHING`, contestID, teamID)
		if err != nil {
			log.Printf("auto join contest team error: %v", err)
			continue
		}

		// 为该队伍生成该比赛的Flag
		if contest.GenerateFlagsForTeamInContest != nil {
			go contest.GenerateFlagsForTeamInContest(db, contestID, teamID, "")
		}

		// 广播队伍列表更新
		go contest.BroadcastFullRefresh(contestID)
	}

	if len(contestIDs) > 0 {
		log.Printf("Team %d auto joined %d contests via organization %d (data import)", teamID, len(contestIDs), orgID)
	}
}

// uniqueStrings 去重字符串切片
func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// HandlePreviewImportUsers 预览导入数据
func HandlePreviewImportUsers(c *gin.Context, db *sql.DB) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_REQUIRED", "message": "请上传Excel文件"})
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "READ_ERROR", "message": "读取文件失败"})
		return
	}

	f, err := excelize.OpenReader(strings.NewReader(string(content)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_FILE", "message": "无法读取Excel文件: " + err.Error()})
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "EMPTY_FILE", "message": "Excel文件为空"})
		return
	}

	rows, err := f.GetRows(sheets[0])
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "READ_ERROR", "message": "读取工作表失败"})
		return
	}

	if len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_DATA", "message": "Excel文件没有数据"})
		return
	}

	header := rows[0]
	colMap := make(map[string]int)
	for i, col := range header {
		col = strings.TrimSpace(strings.ToLower(col))
		switch {
		case col == "用户名" || col == "username":
			colMap["username"] = i
		case col == "真实姓名" || col == "姓名" || col == "displayname" || col == "display_name" || col == "name":
			colMap["displayName"] = i
		case col == "邮箱" || col == "email":
			colMap["email"] = i
		case col == "队伍" || col == "所属队伍" || col == "team" || col == "teamname" || col == "team_name":
			colMap["teamName"] = i
		case col == "组织" || col == "机构" || col == "所属组织" || col == "所属机构" || col == "organization" || col == "org":
			colMap["organization"] = i
		}
	}

	var users []ImportUserRow
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if len(row) == 0 {
			continue
		}

		user := ImportUserRow{}
		if idx, ok := colMap["username"]; ok && idx < len(row) {
			user.Username = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["displayName"]; ok && idx < len(row) {
			user.DisplayName = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["email"]; ok && idx < len(row) {
			user.Email = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["teamName"]; ok && idx < len(row) {
			user.TeamName = strings.TrimSpace(row[idx])
		}
		if idx, ok := colMap["organization"]; ok && idx < len(row) {
			user.Organization = strings.TrimSpace(row[idx])
		}

		if user.Username != "" {
			users = append(users, user)
		}
	}

	// 检查哪些用户名已存在
	existingUsers := make(map[string]bool)
	for _, user := range users {
		var id int64
		err := db.QueryRow(`SELECT id FROM users WHERE username = $1`, user.Username).Scan(&id)
		if err == nil {
			existingUsers[user.Username] = true
		}
	}

	// 检查哪些组织和队伍需要新建
	newOrgs := make(map[string]bool)
	newTeams := make(map[string]bool)
	for _, user := range users {
		if user.Organization != "" {
			var id int64
			err := db.QueryRow(`SELECT id FROM organizations WHERE name = $1`, user.Organization).Scan(&id)
			if err == sql.ErrNoRows {
				newOrgs[user.Organization] = true
			}
		}
		if user.TeamName != "" {
			var id int64
			err := db.QueryRow(`SELECT id FROM teams WHERE name = $1`, user.TeamName).Scan(&id)
			if err == sql.ErrNoRows {
				newTeams[user.TeamName] = true
			}
		}
	}

	newOrgsList := []string{}
	for org := range newOrgs {
		newOrgsList = append(newOrgsList, org)
	}
	newTeamsList := []string{}
	for team := range newTeams {
		newTeamsList = append(newTeamsList, team)
	}
	existingUsersList := []string{}
	for user := range existingUsers {
		existingUsersList = append(existingUsersList, user)
	}

	c.JSON(http.StatusOK, gin.H{
		"users":         users,
		"total":         len(users),
		"existingUsers": existingUsersList,
		"newOrgs":       newOrgsList,
		"newTeams":      newTeamsList,
		"colMap":        colMap,
	})
}

// HandleDownloadImportTemplate 下载导入模板
func HandleDownloadImportTemplate(c *gin.Context, db *sql.DB) {
	f := excelize.NewFile()
	defer f.Close()

	headers := []string{"用户名", "真实姓名", "邮箱", "所属队伍", "所属组织"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Sheet1", cell, h)
	}

	examples := [][]string{
		{"zhangsan", "张三", "zhangsan@example.com", "安全团队A", "信息安全学院"},
		{"lisi", "李四", "", "安全团队A", "信息安全学院"},
		{"wangwu", "王五", "wangwu@example.com", "安全团队B", "计算机学院"},
	}
	for i, row := range examples {
		for j, val := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue("Sheet1", cell, val)
		}
	}

	f.SetColWidth("Sheet1", "A", "A", 15)
	f.SetColWidth("Sheet1", "B", "B", 15)
	f.SetColWidth("Sheet1", "C", "C", 25)
	f.SetColWidth("Sheet1", "D", "D", 20)
	f.SetColWidth("Sheet1", "E", "E", 20)

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=user_import_template.xlsx")

	if err := f.Write(c.Writer); err != nil {
		log.Printf("write excel error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "WRITE_ERROR"})
		return
	}
}

// HandleGetImportStats 获取导入统计
func HandleGetImportStats(c *gin.Context, db *sql.DB) {
	var userCount, teamCount, orgCount int
	db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'user'`).Scan(&userCount)
	db.QueryRow(`SELECT COUNT(*) FROM teams WHERE is_admin_team = FALSE`).Scan(&teamCount)
	db.QueryRow(`SELECT COUNT(*) FROM organizations WHERE name != 'TG_AdminX'`).Scan(&orgCount)

	c.JSON(http.StatusOK, gin.H{
		"userCount": userCount,
		"teamCount": teamCount,
		"orgCount":  orgCount,
	})
}

// HandleImportUsersJSON 用于JSON导入
func HandleImportUsersJSON(c *gin.Context, db *sql.DB) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "READ_ERROR"})
		return
	}

	var req struct {
		Users []ImportUserRow `json:"users"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_JSON", "message": "JSON格式错误"})
		return
	}

	result := processUserImport(db, req.Users)
	c.JSON(http.StatusOK, result)
}
