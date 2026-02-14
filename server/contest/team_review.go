// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package contest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"tgctf/server/monitor"
)

// WebSocket 连接管理（按比赛ID分组）
var (
	auditClients   = make(map[string]map[*websocket.Conn]bool) // contestID -> connections
	auditClientsMu sync.RWMutex
	auditUpgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

// TeamEntry 队伍信息结构
type TeamEntry struct {
	ID              int64  `json:"id"`
	TeamID          int64  `json:"teamId"`
	TeamName        string `json:"teamName"`
	TeamDescription string `json:"teamDescription"`
	OrgID           int64  `json:"orgId,omitempty"`
	OrgName         string `json:"orgName,omitempty"`
	Status          string `json:"status"`
	CreatedAt       string `json:"createdAt"`
}

// HandleAuditWebSocket WebSocket 实时队伍审核推送
func HandleAuditWebSocket(c *gin.Context) {
	contestID := c.Param("id")
	conn, err := auditUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 注册连接
	auditClientsMu.Lock()
	if auditClients[contestID] == nil {
		auditClients[contestID] = make(map[*websocket.Conn]bool)
	}
	auditClients[contestID][conn] = true
	auditClientsMu.Unlock()

	defer func() {
		auditClientsMu.Lock()
		delete(auditClients[contestID], conn)
		if len(auditClients[contestID]) == 0 {
			delete(auditClients, contestID)
		}
		auditClientsMu.Unlock()
	}()

	// 保持连接
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// BroadcastNewTeams 广播新的待审核队伍
func BroadcastNewTeams(contestID string, teams []TeamEntry) {
	data, err := json.Marshal(gin.H{"type": "new_teams", "teams": teams})
	if err != nil {
		return
	}

	auditClientsMu.RLock()
	defer auditClientsMu.RUnlock()

	if clients, ok := auditClients[contestID]; ok {
		for conn := range clients {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

// BroadcastTeamsRemoved 广播被删除的队伍ID列表
func BroadcastTeamsRemoved(contestID string, teamIDs []int64) {
	data, err := json.Marshal(gin.H{"type": "teams_removed", "teamIds": teamIDs})
	if err != nil {
		return
	}

	auditClientsMu.RLock()
	defer auditClientsMu.RUnlock()

	if clients, ok := auditClients[contestID]; ok {
		for conn := range clients {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

// BroadcastFullRefresh 广播完全刷新信号（组织变更后队伍列表需要重新加载）
func BroadcastFullRefresh(contestID string) {
	data, _ := json.Marshal(gin.H{"type": "full_refresh"})

	auditClientsMu.RLock()
	defer auditClientsMu.RUnlock()

	if clients, ok := auditClients[contestID]; ok {
		for conn := range clients {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

// HandleGetContestTeams 获取比赛的队伍列表（包含审核状态）
func HandleGetContestTeams(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	status := c.Query("status") // 可选过滤: pending | approved | rejected

	var query string
	var args []interface{}

	baseQuery := `
		SELECT ct.id, ct.team_id, t.name as team_name, COALESCE(t.description,'') as team_description,
		       o.id as org_id, COALESCE(o.name,'') as org_name,
		       ct.status, ct.reviewed_by, COALESCE(u.username, '') as reviewer_name,
		       ct.reviewed_at, ct.reject_reason, ct.created_at, ct.allocated_ports
		FROM contest_teams ct
		JOIN teams t ON ct.team_id = t.id
		LEFT JOIN organizations o ON t.organization_id = o.id
		LEFT JOIN users u ON ct.reviewed_by = u.id
		WHERE ct.contest_id = $1`

	if status != "" {
		query = baseQuery + " AND ct.status = $2 ORDER BY ct.created_at DESC"
		args = []interface{}{contestID, status}
	} else {
		query = baseQuery + " ORDER BY ct.status, ct.created_at DESC"
		args = []interface{}{contestID}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("query contest teams error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var teams []gin.H
	for rows.Next() {
		var id, teamID int64
		var teamName, teamDescription string
		var orgID sql.NullInt64
		var orgName string
		var teamStatus, reviewerName string
		var reviewedBy sql.NullInt64
		var reviewedAt sql.NullTime
		var rejectReason sql.NullString
		var createdAt time.Time
		var allocatedPorts []byte // PostgreSQL 数组

		if err := rows.Scan(&id, &teamID, &teamName, &teamDescription,
			&orgID, &orgName, &teamStatus, &reviewedBy, &reviewerName,
			&reviewedAt, &rejectReason, &createdAt, &allocatedPorts); err != nil {
			log.Printf("scan contest team error: %v", err)
			continue
		}

		// 解析预分配端口
		var ports []int
		if len(allocatedPorts) > 2 {
			portsStr := string(allocatedPorts[1 : len(allocatedPorts)-1]) // 去掉 { }
			if portsStr != "" {
				parts := strings.Split(portsStr, ",")
				for _, p := range parts {
					if port, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
						ports = append(ports, port)
					}
				}
			}
		}

		team := gin.H{
			"id":              id,
			"teamId":          teamID,
			"teamName":        teamName,
			"teamDescription": teamDescription,
			"status":          teamStatus,
			"createdAt":       createdAt.Format("2006-01-02 15:04:05"),
			"allocatedPorts":  ports,
		}

		if orgID.Valid {
			team["orgId"] = orgID.Int64
			team["orgName"] = orgName
		}
		if reviewedBy.Valid {
			team["reviewedBy"] = reviewedBy.Int64
			team["reviewerName"] = reviewerName
		}
		if reviewedAt.Valid {
			team["reviewedAt"] = reviewedAt.Time.Format("2006-01-02 15:04:05")
		}
		if rejectReason.Valid {
			team["rejectReason"] = rejectReason.String
		}

		teams = append(teams, team)
	}

	if teams == nil {
		teams = []gin.H{}
	}

	c.JSON(http.StatusOK, teams)
}

// HandleReviewContestTeam 审核比赛队伍（通过/拒绝/撤销/封禁/作弊封禁）
func HandleReviewContestTeam(c *gin.Context, db *sql.DB) {
	contestTeamID := c.Param("teamId")

	var req struct {
		Action string `json:"action"` // approve | reject | revoke | ban | cheating_ban
		Reason string `json:"reason"` // 原因（可选）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 支持的操作类型
	validActions := map[string]string{
		"approve":      "approved",
		"reject":       "rejected",
		"revoke":       "pending",         // 撤销通过，重置为待审核
		"ban":          "banned",          // 封禁
		"cheating_ban": "cheating_banned", // 作弊封禁
	}

	newStatus, ok := validActions[req.Action]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ACTION", "message": "不支持的操作类型"})
		return
	}

	// 获取当前用户ID
	userID, _ := c.Get("user_id")

	var reason *string
	if req.Reason != "" {
		reason = &req.Reason
	}

	// 先获取 contestID 和 teamID（用于生成flag）
	var contestID, teamID int64
	err := db.QueryRow(`SELECT contest_id, team_id FROM contest_teams WHERE id = $1`, contestTeamID).Scan(&contestID, &teamID)
	log.Printf("[TeamReview] HandleReviewContestTeam: contestTeamID=%s, contestID=%d, teamID=%d, action=%s, err=%v", contestTeamID, contestID, teamID, req.Action, err)

	// 如果是撤销操作，清除审核信息
	if req.Action == "revoke" {
		_, err = db.Exec(`UPDATE contest_teams 
			SET status = $1, reviewed_by = NULL, reviewed_at = NULL, reject_reason = NULL, updated_at = NOW()
			WHERE id = $2`,
			newStatus, contestTeamID)
	} else {
		_, err = db.Exec(`UPDATE contest_teams 
			SET status = $1, reviewed_by = $2, reviewed_at = NOW(), reject_reason = $3, updated_at = NOW()
			WHERE id = $4`,
			newStatus, userID, reason, contestTeamID)
	}

	if err != nil {
		log.Printf("update contest team status error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 审核通过时，为该队伍生成该比赛所有公开题目的flag
	if req.Action == "approve" && contestID > 0 && teamID > 0 && GenerateFlagsForTeamInContest != nil {
		go GenerateFlagsForTeamInContest(db, fmt.Sprintf("%d", contestID), teamID, "")
	}

	// 封禁操作时，广播大屏事件
	if (req.Action == "ban" || req.Action == "cheating_ban") && contestID > 0 && teamID > 0 {
		var teamName string
		db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID).Scan(&teamName)
		contestIDStr := strconv.FormatInt(contestID, 10)
		log.Printf("[TeamReview] Ban action: action=%s, contestID=%s, teamID=%d, teamName=%s", req.Action, contestIDStr, teamID, teamName)
		if req.Action == "ban" {
			monitor.AddMonitorEventToDB(db, contestIDStr, "ban", teamName, "", "")
		} else {
			monitor.AddMonitorEventToDB(db, contestIDStr, "cheat", teamName, "", "")
		}
		// 广播大屏更新
		go func() {
			data := monitor.GetMonitorDataForBroadcast(db, contestIDStr)
			monitor.BroadcastMonitorUpdate(contestIDStr, data)
		}()
	}

	// 操作成功提示
	messages := map[string]string{
		"approve":      "审核通过",
		"reject":       "审核拒绝",
		"revoke":       "已撤销通过",
		"ban":          "已封禁",
		"cheating_ban": "已作弊封禁",
	}
	c.JSON(http.StatusOK, gin.H{"message": messages[req.Action]})
}

// HandleBatchReviewContestTeams 批量审核比赛队伍
func HandleBatchReviewContestTeams(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	var req struct {
		TeamIDs []int64 `json:"teamIds"`
		Action  string  `json:"action"` // approve | reject | revoke | ban | cheating_ban
		Reason  string  `json:"reason"` // 原因（可选）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 支持的操作类型
	validActions := map[string]string{
		"approve":      "approved",
		"reject":       "rejected",
		"revoke":       "pending",
		"ban":          "banned",
		"cheating_ban": "cheating_banned",
	}

	newStatus, ok := validActions[req.Action]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ACTION", "message": "不支持的操作类型"})
		return
	}

	if len(req.TeamIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_TEAMS_SELECTED"})
		return
	}

	// 获取当前用户ID
	userID, _ := c.Get("user_id")

	var reason *string
	if req.Reason != "" {
		reason = &req.Reason
	}

	successCount := 0
	for _, teamID := range req.TeamIDs {
		var err error
		if req.Action == "revoke" {
			_, err = db.Exec(`UPDATE contest_teams 
				SET status = $1, reviewed_by = NULL, reviewed_at = NULL, reject_reason = NULL, updated_at = NOW()
				WHERE contest_id = $2 AND team_id = $3`,
				newStatus, contestID, teamID)
		} else {
			_, err = db.Exec(`UPDATE contest_teams 
				SET status = $1, reviewed_by = $2, reviewed_at = NOW(), reject_reason = $3, updated_at = NOW()
				WHERE contest_id = $4 AND team_id = $5`,
				newStatus, userID, reason, contestID, teamID)
		}
		if err == nil {
			successCount++
			// 审核通过时，为该队伍生成该比赛所有公开题目的flag
			if req.Action == "approve" && GenerateFlagsForTeamInContest != nil {
				go GenerateFlagsForTeamInContest(db, contestID, teamID, "")
			}
			// 封禁操作时，广播大屏事件
			if req.Action == "ban" || req.Action == "cheating_ban" {
				var teamName string
				db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID).Scan(&teamName)
				if req.Action == "ban" {
					monitor.AddMonitorEventToDB(db, contestID, "ban", teamName, "", "")
				} else {
					monitor.AddMonitorEventToDB(db, contestID, "cheat", teamName, "", "")
				}
			}
		}
	}

	// 批量封禁操作后，一次性广播大屏更新
	if (req.Action == "ban" || req.Action == "cheating_ban") && successCount > 0 {
		go func() {
			data := monitor.GetMonitorDataForBroadcast(db, contestID)
			monitor.BroadcastMonitorUpdate(contestID, data)
		}()
	}

	// 操作成功提示
	messages := map[string]string{
		"approve":      "批量审核通过",
		"reject":       "批量审核拒绝",
		"revoke":       "批量撤销通过",
		"ban":          "批量封禁",
		"cheating_ban": "批量作弊封禁",
	}
	c.JSON(http.StatusOK, gin.H{"message": messages[req.Action], "count": successCount})
}

// HandleCheckTeamStatus 检查用户队伍在比赛中的审核状态
func HandleCheckTeamStatus(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	userID := c.GetInt64("userID")

	// 查询用户所属队伍
	var teamID sql.NullInt64
	err := db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusOK, gin.H{
			"hasTeam":  false,
			"status":   "",
			"message":  "用户不存在",
			"canEnter": false,
		})
		return
	}
	if err != nil {
		log.Printf("query user team error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 用户没有队伍
	if !teamID.Valid {
		c.JSON(http.StatusOK, gin.H{
			"hasTeam":  false,
			"status":   "",
			"message":  "您还没有加入任何队伍",
			"canEnter": false,
		})
		return
	}

	// 检查比赛是否为公开比赛（没有关联组织）
	var orgCount int
	db.QueryRow(`SELECT COUNT(*) FROM contest_organizations WHERE contest_id = $1`, contestID).Scan(&orgCount)
	if orgCount == 0 {
		// 公开比赛，所有人可以进入
		c.JSON(http.StatusOK, gin.H{
			"hasTeam":  true,
			"status":   "approved",
			"message":  "公开比赛",
			"canEnter": true,
			"isPublic": true,
		})
		return
	}

	// 查询队伍在该比赛中的审核状态
	var status string
	err = db.QueryRow(`SELECT status FROM contest_teams WHERE contest_id = $1 AND team_id = $2`, contestID, teamID.Int64).Scan(&status)
	if err == sql.ErrNoRows {
		// 队伍未报名该比赛
		c.JSON(http.StatusOK, gin.H{
			"hasTeam":  true,
			"status":   "",
			"message":  "您的队伍未报名该比赛",
			"canEnter": false,
		})
		return
	}
	if err != nil {
		log.Printf("query team status error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 根据状态返回信息
	statusMessages := map[string]string{
		"pending":         "队伍审核中，请等待审核通过",
		"approved":        "队伍已通过审核",
		"rejected":        "队伍审核未通过",
		"banned":          "队伍已被封禁",
		"cheating_banned": "队伍因作弊被封禁",
	}

	message, ok := statusMessages[status]
	if !ok {
		message = "未知状态"
	}

	c.JSON(http.StatusOK, gin.H{
		"hasTeam":  true,
		"status":   status,
		"message":  message,
		"canEnter": status == "approved",
	})
}

// AllocatePortsFunc 端口分配函数（由 main.go 注入）
var AllocatePortsFunc func(db *sql.DB, count int) ([]int, error)

// HandleAllocatePorts 手动触发端口预分配
func HandleAllocatePorts(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	log.Printf("[端口预分配] 开始为比赛 %s 分配端口", contestID)

	// 1. 获取比赛配置
	var contestMode string
	var containerLimit int
	err := db.QueryRow(`SELECT mode, COALESCE(container_limit, 1) FROM contests WHERE id = $1`, contestID).Scan(&contestMode, &containerLimit)
	if err != nil {
		log.Printf("[端口预分配] 获取比赛配置失败: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "CONTEST_NOT_FOUND"})
		return
	}
	if containerLimit <= 0 {
		containerLimit = 1
	}

	// 2. 获取题目端口需求（取最大值）
	var maxPortsPerChallenge int
	var challengeQuery string
	if contestMode == "awd-f" {
		challengeQuery = `SELECT q.ports FROM contest_challenges_awdf cc 
			JOIN question_bank_awdf q ON cc.question_id = q.id 
			WHERE cc.contest_id = $1 AND q.docker_image IS NOT NULL AND q.docker_image != ''`
	} else {
		challengeQuery = `SELECT q.ports FROM contest_challenges cc 
			JOIN question_bank q ON cc.question_id = q.id 
			WHERE cc.contest_id = $1 AND q.docker_image IS NOT NULL AND q.docker_image != ''`
	}

	challengeRows, err := db.Query(challengeQuery, contestID)
	if err == nil {
		defer challengeRows.Close()
		for challengeRows.Next() {
			var portsJSON sql.NullString
			challengeRows.Scan(&portsJSON)
			if portsJSON.Valid && portsJSON.String != "" {
				var portList []string
				if json.Unmarshal([]byte(portsJSON.String), &portList) == nil {
					if len(portList) > maxPortsPerChallenge {
						maxPortsPerChallenge = len(portList)
					}
				}
			}
		}
	}

	if maxPortsPerChallenge == 0 {
		// 默认每个容器需要 2 个端口（22 + Web）
		maxPortsPerChallenge = 2
	}

	portsPerTeam := maxPortsPerChallenge * containerLimit
	log.Printf("[端口预分配] 比赛 %s: 每队伍需要 %d 个端口", contestID, portsPerTeam)

	// 3. 获取所有已审核队伍
	teamRows, err := db.Query(`
		SELECT ct.team_id, t.name, ct.allocated_ports
		FROM contest_teams ct
		JOIN teams t ON ct.team_id = t.id
		WHERE ct.contest_id = $1 AND ct.status = 'approved'
		ORDER BY ct.team_id
	`, contestID)
	if err != nil {
		log.Printf("[端口预分配] 获取队伍列表失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer teamRows.Close()

	type TeamInfo struct {
		ID             int64
		Name           string
		AllocatedPorts []int
	}
	var teams []TeamInfo
	for teamRows.Next() {
		var t TeamInfo
		var portsArray []byte
		if err := teamRows.Scan(&t.ID, &t.Name, &portsArray); err != nil {
			continue
		}
		// 解析 PostgreSQL 数组格式
		if len(portsArray) > 2 {
			portsStr := string(portsArray[1 : len(portsArray)-1])
			if portsStr != "" {
				parts := strings.Split(portsStr, ",")
				for _, p := range parts {
					if port, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
						t.AllocatedPorts = append(t.AllocatedPorts, port)
					}
				}
			}
		}
		teams = append(teams, t)
	}

	if len(teams) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "没有已审核的队伍", "allocated": 0})
		return
	}

	// 4. 为每个队伍分配端口
	if AllocatePortsFunc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PORT_ALLOCATOR_NOT_INITIALIZED"})
		return
	}

	allocatedCount := 0
	for _, team := range teams {
		// 检查是否已有足够端口
		if len(team.AllocatedPorts) >= portsPerTeam {
			log.Printf("[端口预分配] 队伍 %s 已有 %d 个端口，跳过", team.Name, len(team.AllocatedPorts))
			continue
		}

		// 需要追加的端口数
		needed := portsPerTeam - len(team.AllocatedPorts)
		newPorts, err := AllocatePortsFunc(db, needed)
		if err != nil {
			log.Printf("[端口预分配] 为队伍 %s 分配端口失败: %v", team.Name, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "PORT_ALLOCATION_FAILED", "message": fmt.Sprintf("为队伍 %s 分配端口失败: %v", team.Name, err)})
			return
		}

		// 合并旧端口和新端口
		allPorts := append(team.AllocatedPorts, newPorts...)

		// 更新数据库
		portsStr := "{" + intSliceToString(allPorts) + "}"
		_, err = db.Exec(`UPDATE contest_teams SET allocated_ports = $1, updated_at = NOW() WHERE contest_id = $2 AND team_id = $3`,
			portsStr, contestID, team.ID)
		if err != nil {
			log.Printf("[端口预分配] 保存队伍 %s 端口失败: %v", team.Name, err)
			continue
		}

		log.Printf("[端口预分配] 队伍 %s 分配端口: %v", team.Name, allPorts)
		allocatedCount++
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       fmt.Sprintf("成功为 %d 支队伍分配端口", allocatedCount),
		"allocated":     allocatedCount,
		"portsPerTeam":  portsPerTeam,
		"totalTeams":    len(teams),
	})
}

// intSliceToString 将 int 切片转为逗号分隔的字符串
func intSliceToString(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	result := make([]string, len(nums))
	for i, n := range nums {
		result[i] = strconv.Itoa(n)
	}
	return strings.Join(result, ",")
}
