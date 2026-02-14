package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WebSocket 连接管理
var (
	antiCheatClients   = make(map[*websocket.Conn]bool)
	antiCheatClientsMu sync.RWMutex
	antiCheatUpgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

// SuspiciousRecord 可疑记录
type SuspiciousRecord struct {
	ID            int64   `json:"id"`
	Type          string  `json:"type"`          // same_ip_diff_team | same_team_multi_ip | same_wrong_flag | login_ip_change | time_anomaly
	RiskLevel     string  `json:"riskLevel"`     // high | medium | low
	ContestID     int64   `json:"contestId"`
	ContestName   string  `json:"contestName"`
	ChallengeID   int64   `json:"challengeId"`
	ChallengeName string  `json:"challengeName"`
	Description   string  `json:"description"`
	Details       []SuspiciousDetail `json:"details"`
	CreatedAt     string  `json:"createdAt"`
}

// SuspiciousDetail 可疑记录详情
type SuspiciousDetail struct {
	TeamID      int64  `json:"teamId"`
	TeamName    string `json:"teamName"`
	UserID      int64  `json:"userId"`
	UserName    string `json:"userName"`
	IPAddress   string `json:"ipAddress"`
	Flag        string `json:"flag"`
	IsCorrect   bool   `json:"isCorrect"`
	SubmittedAt string `json:"submittedAt"`
}

// IPAnalysis IP分析结果
type IPAnalysis struct {
	IPAddress   string       `json:"ipAddress"`
	Teams       []TeamIPInfo `json:"teams"`
	TotalCount  int          `json:"totalCount"`
}

// TeamIPInfo 队伍IP信息
type TeamIPInfo struct {
	TeamID        int64  `json:"teamId"`
	TeamName      string `json:"teamName"`
	SubmitCount   int    `json:"submitCount"`
	CorrectCount  int    `json:"correctCount"`
	LastSubmitAt  string `json:"lastSubmitAt"`
}

// TeamIPTrack 队伍IP轨迹
type TeamIPTrack struct {
	TeamID    int64        `json:"teamId"`
	TeamName  string       `json:"teamName"`
	IPRecords []IPRecord   `json:"ipRecords"`
}

// IPRecord IP记录
type IPRecord struct {
	IPAddress    string `json:"ipAddress"`
	SubmitCount  int    `json:"submitCount"`
	FirstSeen    string `json:"firstSeen"`
	LastSeen     string `json:"lastSeen"`
}

// HandleGetSuspiciousRecords 获取可疑记录列表
func HandleGetSuspiciousRecords(c *gin.Context, db *sql.DB) {
	contestID := c.Query("contestId")
	riskLevel := c.Query("riskLevel")
	
	var records []SuspiciousRecord
	
	// 1. 检测同IP不同队伍提交正确答案（高风险）
	sameIPQuery := `
		SELECT s.ip_address, s.challenge_id, cc.contest_id, 
		       q.title as challenge_name, c.name as contest_name,
		       COUNT(DISTINCT s.team_id) as team_count
		FROM submissions s
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		JOIN question_bank q ON cc.question_id = q.id
		JOIN contests c ON cc.contest_id = c.id
		WHERE s.is_correct = true AND s.ip_address IS NOT NULL AND s.ip_address != ''
	`
	if contestID != "" {
		sameIPQuery += " AND cc.contest_id = " + contestID
	}
	sameIPQuery += `
		GROUP BY s.ip_address, s.challenge_id, cc.contest_id, q.title, c.name
		HAVING COUNT(DISTINCT s.team_id) > 1
		ORDER BY team_count DESC
		LIMIT 50
	`
	
	if riskLevel == "" || riskLevel == "high" {
		rows, err := db.Query(sameIPQuery)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var ip string
				var challengeID, cID int64
				var challengeName, contestName string
				var teamCount int
				rows.Scan(&ip, &challengeID, &cID, &challengeName, &contestName, &teamCount)
				
				// 获取详情
				details := getSameIPDetails(db, ip, challengeID)
				
				records = append(records, SuspiciousRecord{
					Type:          "same_ip_diff_team",
					RiskLevel:     "high",
					ContestID:     cID,
					ContestName:   contestName,
					ChallengeID:   challengeID,
					ChallengeName: challengeName,
					Description:   "同一IP(" + ip + ")有" + strconv.Itoa(teamCount) + "个不同队伍提交了正确答案",
					Details:       details,
				})
			}
		}
	}
	
	// 2. 检测不同队伍提交相同错误flag（高风险）
	sameWrongFlagQuery := `
		SELECT s.flag, s.challenge_id, cc.contest_id,
		       q.title as challenge_name, c.name as contest_name,
		       COUNT(DISTINCT s.team_id) as team_count
		FROM submissions s
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		JOIN question_bank q ON cc.question_id = q.id
		JOIN contests c ON cc.contest_id = c.id
		WHERE s.is_correct = false AND s.is_cheating = false
		  AND LENGTH(s.flag) > 10
	`
	if contestID != "" {
		sameWrongFlagQuery += " AND cc.contest_id = " + contestID
	}
	sameWrongFlagQuery += `
		GROUP BY s.flag, s.challenge_id, cc.contest_id, q.title, c.name
		HAVING COUNT(DISTINCT s.team_id) > 1
		ORDER BY team_count DESC
		LIMIT 50
	`
	
	if riskLevel == "" || riskLevel == "high" {
		rows, err := db.Query(sameWrongFlagQuery)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var flag string
				var challengeID, cID int64
				var challengeName, contestName string
				var teamCount int
				rows.Scan(&flag, &challengeID, &cID, &challengeName, &contestName, &teamCount)
				
				// 获取详情
				details := getSameWrongFlagDetails(db, flag, challengeID)
				
				// 截断显示的flag
				displayFlag := flag
				if len(displayFlag) > 30 {
					displayFlag = displayFlag[:30] + "..."
				}
				
				records = append(records, SuspiciousRecord{
					Type:          "same_wrong_flag",
					RiskLevel:     "high",
					ContestID:     cID,
					ContestName:   contestName,
					ChallengeID:   challengeID,
					ChallengeName: challengeName,
					Description:   strconv.Itoa(teamCount) + "个队伍提交了相同的错误Flag: " + displayFlag,
					Details:       details,
				})
			}
		}
	}
	
	// 3. 检测同一队伍多IP提交（中风险）
	multiIPQuery := `
		SELECT s.team_id, t.name as team_name, cc.contest_id, c.name as contest_name,
		       COUNT(DISTINCT s.ip_address) as ip_count
		FROM submissions s
		JOIN teams t ON s.team_id = t.id
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		JOIN contests c ON cc.contest_id = c.id
		WHERE s.ip_address IS NOT NULL AND s.ip_address != ''
	`
	if contestID != "" {
		multiIPQuery += " AND cc.contest_id = " + contestID
	}
	multiIPQuery += `
		GROUP BY s.team_id, t.name, cc.contest_id, c.name
		HAVING COUNT(DISTINCT s.ip_address) > 3
		ORDER BY ip_count DESC
		LIMIT 50
	`
	
	if riskLevel == "" || riskLevel == "medium" {
		rows, err := db.Query(multiIPQuery)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var teamID, cID int64
				var teamName, contestName string
				var ipCount int
				rows.Scan(&teamID, &teamName, &cID, &contestName, &ipCount)
				
				// 获取该队伍的IP列表
				details := getTeamIPDetails(db, teamID, cID)
				
				records = append(records, SuspiciousRecord{
					Type:          "same_team_multi_ip",
					RiskLevel:     "medium",
					ContestID:     cID,
					ContestName:   contestName,
					Description:   "队伍[" + teamName + "]使用了" + strconv.Itoa(ipCount) + "个不同IP提交答案",
					Details:       details,
				})
			}
		}
	}
	
	// 4. 检测用户登录IP变化（中风险）- 与上次登录IP不同
	if riskLevel == "" || riskLevel == "medium" {
		// 查询最近7天内IP变化的用户
		ipChangeQuery := `
			WITH recent_logins AS (
				SELECT user_id, ip_address, login_at,
				       LAG(ip_address) OVER (PARTITION BY user_id ORDER BY login_at) as prev_ip,
				       LAG(login_at) OVER (PARTITION BY user_id ORDER BY login_at) as prev_login_at
				FROM user_login_history
				WHERE login_at > NOW() - INTERVAL '7 days'
			)
			SELECT rl.user_id, u.display_name, u.username, t.id as team_id, t.name as team_name,
			       rl.prev_ip, rl.ip_address as new_ip, rl.prev_login_at, rl.login_at
			FROM recent_logins rl
			JOIN users u ON rl.user_id = u.id
			LEFT JOIN teams t ON u.team_id = t.id
			WHERE rl.prev_ip IS NOT NULL 
			  AND rl.ip_address != rl.prev_ip
			  AND u.role = 'user'
			ORDER BY rl.login_at DESC
			LIMIT 100
		`
		
		rows, err := db.Query(ipChangeQuery)
		if err == nil {
			defer rows.Close()
			// 按用户分组聚合
			ipChangeUsers := make(map[int64]*struct {
				UserID      int64
				DisplayName string
				Username    string
				TeamID      sql.NullInt64
				TeamName    sql.NullString
				Changes     []struct {
					PrevIP    string
					NewIP     string
					PrevTime  string
					LoginTime string
				}
			})
			
			for rows.Next() {
				var userID int64
				var displayName, username string
				var teamID sql.NullInt64
				var teamName sql.NullString
				var prevIP, newIP, prevTime, loginTime string
				rows.Scan(&userID, &displayName, &username, &teamID, &teamName, &prevIP, &newIP, &prevTime, &loginTime)
				
				if _, ok := ipChangeUsers[userID]; !ok {
					ipChangeUsers[userID] = &struct {
						UserID      int64
						DisplayName string
						Username    string
						TeamID      sql.NullInt64
						TeamName    sql.NullString
						Changes     []struct {
							PrevIP    string
							NewIP     string
							PrevTime  string
							LoginTime string
						}
					}{
						UserID:      userID,
						DisplayName: displayName,
						Username:    username,
						TeamID:      teamID,
						TeamName:    teamName,
					}
				}
				ipChangeUsers[userID].Changes = append(ipChangeUsers[userID].Changes, struct {
					PrevIP    string
					NewIP     string
					PrevTime  string
					LoginTime string
				}{prevIP, newIP, prevTime, loginTime})
			}
			
			// 生成可疑记录
			for _, user := range ipChangeUsers {
				var details []SuspiciousDetail
				for _, change := range user.Changes {
					details = append(details, SuspiciousDetail{
						UserID:      user.UserID,
						UserName:    user.DisplayName,
						TeamID:      user.TeamID.Int64,
						TeamName:    user.TeamName.String,
						IPAddress:   change.PrevIP + " → " + change.NewIP,
						SubmittedAt: change.LoginTime,
					})
				}
				
				teamInfo := ""
				if user.TeamName.Valid {
					teamInfo = "，队伍[" + user.TeamName.String + "]"
				}
				
				records = append(records, SuspiciousRecord{
					Type:        "login_ip_change",
					RiskLevel:   "medium",
					Description: "用户[" + user.DisplayName + "]" + teamInfo + " 登录IP发生变化（" + strconv.Itoa(len(user.Changes)) + "次）",
					Details:     details,
				})
			}
		}
	}
	
	if records == nil {
		records = []SuspiciousRecord{}
	}
	
	c.JSON(http.StatusOK, records)
}

// HandleGetIPAnalysis 获取IP分析
func HandleGetIPAnalysis(c *gin.Context, db *sql.DB) {
	contestID := c.Query("contestId")
	ip := c.Query("ip")
	
	if ip == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "IP_REQUIRED"})
		return
	}
	
	query := `
		SELECT s.team_id, t.name as team_name,
		       COUNT(*) as submit_count,
		       SUM(CASE WHEN s.is_correct THEN 1 ELSE 0 END) as correct_count,
		       MAX(s.submitted_at) as last_submit
		FROM submissions s
		JOIN teams t ON s.team_id = t.id
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		WHERE s.ip_address = $1
	`
	args := []interface{}{ip}
	if contestID != "" {
		query += " AND cc.contest_id = $2"
		args = append(args, contestID)
	}
	query += `
		GROUP BY s.team_id, t.name
		ORDER BY submit_count DESC
	`
	
	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()
	
	var teams []TeamIPInfo
	totalCount := 0
	for rows.Next() {
		var info TeamIPInfo
		rows.Scan(&info.TeamID, &info.TeamName, &info.SubmitCount, &info.CorrectCount, &info.LastSubmitAt)
		teams = append(teams, info)
		totalCount += info.SubmitCount
	}
	
	if teams == nil {
		teams = []TeamIPInfo{}
	}
	
	c.JSON(http.StatusOK, IPAnalysis{
		IPAddress:  ip,
		Teams:      teams,
		TotalCount: totalCount,
	})
}

// HandleGetTeamIPTrack 获取队伍IP轨迹
func HandleGetTeamIPTrack(c *gin.Context, db *sql.DB) {
	teamID := c.Param("teamId")
	contestID := c.Query("contestId")
	
	// 获取队伍信息
	var teamName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID).Scan(&teamName)
	
	query := `
		SELECT s.ip_address,
		       COUNT(*) as submit_count,
		       MIN(s.submitted_at) as first_seen,
		       MAX(s.submitted_at) as last_seen
		FROM submissions s
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		WHERE s.team_id = $1 AND s.ip_address IS NOT NULL AND s.ip_address != ''
	`
	args := []interface{}{teamID}
	if contestID != "" {
		query += " AND cc.contest_id = $2"
		args = append(args, contestID)
	}
	query += `
		GROUP BY s.ip_address
		ORDER BY last_seen DESC
	`
	
	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()
	
	var ipRecords []IPRecord
	for rows.Next() {
		var record IPRecord
		rows.Scan(&record.IPAddress, &record.SubmitCount, &record.FirstSeen, &record.LastSeen)
		ipRecords = append(ipRecords, record)
	}
	
	if ipRecords == nil {
		ipRecords = []IPRecord{}
	}
	
	teamIDInt, _ := strconv.ParseInt(teamID, 10, 64)
	c.JSON(http.StatusOK, TeamIPTrack{
		TeamID:    teamIDInt,
		TeamName:  teamName,
		IPRecords: ipRecords,
	})
}

// HandleBanTeamForCheating 封禁队伍（作弊）
func HandleBanTeamForCheating(c *gin.Context, db *sql.DB) {
	var req struct {
		TeamID    int64  `json:"teamId"`
		ContestID int64  `json:"contestId"`
		Reason    string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	
	// 更新队伍状态为作弊封禁
	_, err := db.Exec(`UPDATE contest_teams SET status = 'cheating_banned' WHERE contest_id = $1 AND team_id = $2`,
		req.ContestID, req.TeamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	
	// 获取队伍名称用于日志
	var teamName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, req.TeamID).Scan(&teamName)
	
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "队伍 [" + teamName + "] 已被封禁",
	})
}

// HandleUnbanTeam 解除队伍封禁
func HandleUnbanTeam(c *gin.Context, db *sql.DB) {
	var req struct {
		TeamID    int64 `json:"teamId"`
		ContestID int64 `json:"contestId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	
	// 恢复队伍状态为已通过
	_, err := db.Exec(`UPDATE contest_teams SET status = 'approved' WHERE contest_id = $1 AND team_id = $2`,
		req.ContestID, req.TeamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	
	var teamName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, req.TeamID).Scan(&teamName)
	
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "队伍 [" + teamName + "] 已解除封禁",
	})
}

// HandleGetCheatingBannedTeams 获取已封禁的作弊队伍列表
func HandleGetCheatingBannedTeams(c *gin.Context, db *sql.DB) {
	contestID := c.Query("contestId")
	
	query := `
		SELECT ct.team_id, t.name as team_name, ct.contest_id, c.name as contest_name,
		       ct.updated_at
		FROM contest_teams ct
		JOIN teams t ON ct.team_id = t.id
		JOIN contests c ON ct.contest_id = c.id
		WHERE ct.status = 'cheating_banned'
	`
	if contestID != "" {
		query += " AND ct.contest_id = " + contestID
	}
	query += " ORDER BY ct.updated_at DESC"
	
	rows, err := db.Query(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()
	
	type BannedTeam struct {
		TeamID      int64  `json:"teamId"`
		TeamName    string `json:"teamName"`
		ContestID   int64  `json:"contestId"`
		ContestName string `json:"contestName"`
		BannedAt    string `json:"bannedAt"`
	}
	
	var teams []BannedTeam
	for rows.Next() {
		var team BannedTeam
		rows.Scan(&team.TeamID, &team.TeamName, &team.ContestID, &team.ContestName, &team.BannedAt)
		teams = append(teams, team)
	}
	
	if teams == nil {
		teams = []BannedTeam{}
	}
	
	c.JSON(http.StatusOK, teams)
}

// 辅助函数：获取同IP提交详情
func getSameIPDetails(db *sql.DB, ip string, challengeID int64) []SuspiciousDetail {
	rows, err := db.Query(`
		SELECT s.team_id, t.name, s.user_id, COALESCE(u.display_name, u.username),
		       s.ip_address, s.flag, s.is_correct, s.submitted_at
		FROM submissions s
		JOIN teams t ON s.team_id = t.id
		JOIN users u ON s.user_id = u.id
		WHERE s.ip_address = $1 AND s.challenge_id = $2 AND s.is_correct = true
		ORDER BY s.submitted_at
	`, ip, challengeID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	
	var details []SuspiciousDetail
	for rows.Next() {
		var d SuspiciousDetail
		rows.Scan(&d.TeamID, &d.TeamName, &d.UserID, &d.UserName, &d.IPAddress, &d.Flag, &d.IsCorrect, &d.SubmittedAt)
		details = append(details, d)
	}
	return details
}

// 辅助函数：获取相同错误flag提交详情
func getSameWrongFlagDetails(db *sql.DB, flag string, challengeID int64) []SuspiciousDetail {
	rows, err := db.Query(`
		SELECT s.team_id, t.name, s.user_id, COALESCE(u.display_name, u.username),
		       s.ip_address, s.flag, s.is_correct, s.submitted_at
		FROM submissions s
		JOIN teams t ON s.team_id = t.id
		JOIN users u ON s.user_id = u.id
		WHERE s.flag = $1 AND s.challenge_id = $2
		ORDER BY s.submitted_at
	`, flag, challengeID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	
	var details []SuspiciousDetail
	for rows.Next() {
		var d SuspiciousDetail
		rows.Scan(&d.TeamID, &d.TeamName, &d.UserID, &d.UserName, &d.IPAddress, &d.Flag, &d.IsCorrect, &d.SubmittedAt)
		details = append(details, d)
	}
	return details
}

// 辅助函数：获取队伍IP详情
func getTeamIPDetails(db *sql.DB, teamID, contestID int64) []SuspiciousDetail {
	rows, err := db.Query(`
		SELECT DISTINCT s.ip_address, s.user_id, COALESCE(u.display_name, u.username),
		       MIN(s.submitted_at) as first_submit
		FROM submissions s
		JOIN users u ON s.user_id = u.id
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		WHERE s.team_id = $1 AND cc.contest_id = $2 AND s.ip_address IS NOT NULL
		GROUP BY s.ip_address, s.user_id, u.display_name, u.username
		ORDER BY first_submit
	`, teamID, contestID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	
	var details []SuspiciousDetail
	for rows.Next() {
		var d SuspiciousDetail
		d.TeamID = teamID
		rows.Scan(&d.IPAddress, &d.UserID, &d.UserName, &d.SubmittedAt)
		details = append(details, d)
	}
	return details
}

// ==================== WebSocket 实时推送 ====================

// HandleAntiCheatWebSocket WebSocket 连接处理
func HandleAntiCheatWebSocket(c *gin.Context) {
	conn, err := antiCheatUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 注册连接
	antiCheatClientsMu.Lock()
	antiCheatClients[conn] = true
	antiCheatClientsMu.Unlock()

	defer func() {
		antiCheatClientsMu.Lock()
		delete(antiCheatClients, conn)
		antiCheatClientsMu.Unlock()
	}()

	// 保持连接
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// BroadcastSuspiciousRecord 广播可疑记录到所有连接的管理员
func BroadcastSuspiciousRecord(record SuspiciousRecord) {
	record.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	
	data, err := json.Marshal(gin.H{
		"type":   "suspicious",
		"record": record,
	})
	if err != nil {
		return
	}

	antiCheatClientsMu.RLock()
	defer antiCheatClientsMu.RUnlock()

	for conn := range antiCheatClients {
		conn.WriteMessage(websocket.TextMessage, data)
	}
}

// CheckAndBroadcastSameIPDiffTeam 检测同IP不同队伍提交正确答案
func CheckAndBroadcastSameIPDiffTeam(db *sql.DB, ip string, challengeID, contestID int64, teamID int64) {
	// 查询同IP同题目正确提交的不同队伍数
	var teamCount int
	err := db.QueryRow(`
		SELECT COUNT(DISTINCT team_id) 
		FROM submissions 
		WHERE ip_address = $1 AND challenge_id = $2 AND is_correct = true
	`, ip, challengeID).Scan(&teamCount)
	
	if err != nil || teamCount <= 1 {
		return
	}

	// 获取题目和比赛信息
	var challengeName, contestName string
	db.QueryRow(`
		SELECT q.title, c.name
		FROM contest_challenges cc
		JOIN question_bank q ON cc.question_id = q.id
		JOIN contests c ON cc.contest_id = c.id
		WHERE cc.id = $1
	`, challengeID).Scan(&challengeName, &contestName)

	details := getSameIPDetails(db, ip, challengeID)

	BroadcastSuspiciousRecord(SuspiciousRecord{
		Type:          "same_ip_diff_team",
		RiskLevel:     "high",
		ContestID:     contestID,
		ContestName:   contestName,
		ChallengeID:   challengeID,
		ChallengeName: challengeName,
		Description:   "同一IP(" + ip + ")有" + strconv.Itoa(teamCount) + "个不同队伍提交了正确答案",
		Details:       details,
	})
}

// CheckAndBroadcastSameWrongFlag 检测不同队伍提交相同错误Flag
func CheckAndBroadcastSameWrongFlag(db *sql.DB, flag string, challengeID, contestID int64) {
	// 查询提交相同flag的不同队伍数
	var teamCount int
	err := db.QueryRow(`
		SELECT COUNT(DISTINCT team_id) 
		FROM submissions 
		WHERE flag = $1 AND challenge_id = $2 AND is_correct = false
	`, flag, challengeID).Scan(&teamCount)
	
	if err != nil || teamCount <= 1 {
		return
	}

	// 获取题目和比赛信息
	var challengeName, contestName string
	db.QueryRow(`
		SELECT q.title, c.name
		FROM contest_challenges cc
		JOIN question_bank q ON cc.question_id = q.id
		JOIN contests c ON cc.contest_id = c.id
		WHERE cc.id = $1
	`, challengeID).Scan(&challengeName, &contestName)

	details := getSameWrongFlagDetails(db, flag, challengeID)

	displayFlag := flag
	if len(displayFlag) > 30 {
		displayFlag = displayFlag[:30] + "..."
	}

	BroadcastSuspiciousRecord(SuspiciousRecord{
		Type:          "same_wrong_flag",
		RiskLevel:     "high",
		ContestID:     contestID,
		ContestName:   contestName,
		ChallengeID:   challengeID,
		ChallengeName: challengeName,
		Description:   strconv.Itoa(teamCount) + "个队伍提交了相同的错误Flag: " + displayFlag,
		Details:       details,
	})
}

// CheckAndBroadcastMultiIP 检测同一队伍多IP提交
func CheckAndBroadcastMultiIP(db *sql.DB, teamID, contestID int64) {
	// 查询该队伍在该比赛中使用的不同IP数
	var ipCount int
	err := db.QueryRow(`
		SELECT COUNT(DISTINCT s.ip_address)
		FROM submissions s
		JOIN contest_challenges cc ON s.challenge_id = cc.id
		WHERE s.team_id = $1 AND cc.contest_id = $2 
		  AND s.ip_address IS NOT NULL AND s.ip_address != ''
	`, teamID, contestID).Scan(&ipCount)
	
	// 只有当IP数刚好达到阈值时才推送（避免重复推送）
	if err != nil || ipCount != 4 {
		return
	}

	// 获取队伍和比赛信息
	var teamName, contestName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID).Scan(&teamName)
	db.QueryRow(`SELECT name FROM contests WHERE id = $1`, contestID).Scan(&contestName)

	details := getTeamIPDetails(db, teamID, contestID)

	BroadcastSuspiciousRecord(SuspiciousRecord{
		Type:        "same_team_multi_ip",
		RiskLevel:   "medium",
		ContestID:   contestID,
		ContestName: contestName,
		Description: "队伍[" + teamName + "]使用了" + strconv.Itoa(ipCount) + "个不同IP提交答案",
		Details:     details,
	})
}

// BroadcastLoginIPChange 广播登录IP变化
func BroadcastLoginIPChange(db *sql.DB, userID int64, displayName, prevIP, newIP string) {
	// 获取用户队伍信息
	var teamID sql.NullInt64
	var teamName sql.NullString
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if teamID.Valid {
		db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
	}

	teamInfo := ""
	if teamName.Valid {
		teamInfo = "，队伍[" + teamName.String + "]"
	}

	details := []SuspiciousDetail{{
		UserID:      userID,
		UserName:    displayName,
		TeamID:      teamID.Int64,
		TeamName:    teamName.String,
		IPAddress:   prevIP + " → " + newIP,
		SubmittedAt: time.Now().Format("2006-01-02 15:04:05"),
	}}

	BroadcastSuspiciousRecord(SuspiciousRecord{
		Type:        "login_ip_change",
		RiskLevel:   "medium",
		Description: "用户[" + displayName + "]" + teamInfo + " 登录IP发生变化",
		Details:     details,
	})
}
