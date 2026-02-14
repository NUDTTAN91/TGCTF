// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package monitor

import (
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

// WebSocket 连接管理（按比赛ID分组）
var (
	monitorClients  = make(map[string]map[*websocket.Conn]bool) // contestID -> connections
	monitorMutex    sync.RWMutex
	monitorUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

// 事件结构（尝试解题、封禁等）
type MonitorEvent struct {
	Type          string `json:"type"`          // attempt, ban, cheat
	TeamName      string `json:"teamName"`
	UserName      string `json:"userName,omitempty"`
	ChallengeName string `json:"challengeName,omitempty"`
	Time          string `json:"time"`
}

// AddMonitorEventToDB 添加大屏事件到数据库并实时推送
func AddMonitorEventToDB(db *sql.DB, contestID string, eventType string, teamName string, userName string, challengeName string) {
	log.Printf("[Monitor] AddMonitorEvent: contestID=%s, type=%s, team=%s, user=%s, challenge=%s", contestID, eventType, teamName, userName, challengeName)
	_, err := db.Exec(`INSERT INTO monitor_events (contest_id, event_type, team_name, user_name, challenge_name) VALUES ($1, $2, $3, $4, $5)`,
		contestID, eventType, teamName, userName, challengeName)
	if err != nil {
		log.Printf("[Monitor] AddMonitorEvent error: %v", err)
		return
	}

	// 实时推送新事件到 WebSocket 客户端
	newEvent := MonitorEvent{
		Type:          eventType,
		TeamName:      teamName,
		UserName:      userName,
		ChallengeName: challengeName,
		Time:          time.Now().Format("2006-01-02 15:04:05"),
	}
	BroadcastMonitorUpdate(contestID, map[string]interface{}{
		"type":   "event",
		"event":  newEvent,
		"events": []MonitorEvent{newEvent},
	})
}

// GetMonitorEventsFromDB 从数据库获取大屏事件
func GetMonitorEventsFromDB(db *sql.DB, contestID string) []MonitorEvent {
	rows, err := db.Query(`
		SELECT event_type, team_name, COALESCE(user_name, ''), COALESCE(challenge_name, ''), TO_CHAR(created_at, 'YYYY-MM-DD HH24:MI:SS')
		FROM monitor_events 
		WHERE contest_id = $1
		ORDER BY created_at DESC 
		LIMIT 50`, contestID)
	if err != nil {
		log.Printf("[Monitor] GetMonitorEvents error: %v", err)
		return []MonitorEvent{}
	}
	defer rows.Close()

	var events []MonitorEvent
	for rows.Next() {
		var e MonitorEvent
		if err := rows.Scan(&e.Type, &e.TeamName, &e.UserName, &e.ChallengeName, &e.Time); err != nil {
			continue
		}
		events = append(events, e)
	}
	if events == nil {
		return []MonitorEvent{}
	}
	return events
}

// HandleMonitorWebSocket WebSocket 实时大屏推送
func HandleMonitorWebSocket(c *gin.Context, jwtSecret []byte) {
	contestID := c.Param("id")
	
	// 从 URL 参数获取 token 并验证
	tokenStr := c.Query("token")
	if tokenStr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "MISSING_TOKEN"})
		return
	}
	
	// 验证 JWT token
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_TOKEN"})
		return
	}
	
	conn, err := monitorUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	monitorMutex.Lock()
	if monitorClients[contestID] == nil {
		monitorClients[contestID] = make(map[*websocket.Conn]bool)
	}
	monitorClients[contestID][conn] = true
	monitorMutex.Unlock()

	defer func() {
		monitorMutex.Lock()
		delete(monitorClients[contestID], conn)
		monitorMutex.Unlock()
	}()

	// 保持连接，等待客户端断开
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// BroadcastMonitorUpdate 广播大屏更新（新解题时调用）
func BroadcastMonitorUpdate(contestID string, data interface{}) {
	monitorMutex.RLock()
	clients := monitorClients[contestID]
	monitorMutex.RUnlock()

	log.Printf("[Monitor] BroadcastMonitorUpdate: contestID=%s, clients=%d", contestID, len(clients))

	if len(clients) == 0 {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	monitorMutex.Lock()
	for conn := range clients {
		err := conn.WriteMessage(websocket.TextMessage, jsonData)
		if err != nil {
			conn.Close()
			delete(clients, conn)
		}
	}
	monitorMutex.Unlock()
}

// formatDuration 格式化时间差为可读字符串
func formatDuration(d time.Duration) string {
	if d < 0 {
		return ""
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
	}
	if minutes > 0 {
		return strconv.Itoa(minutes) + "m " + strconv.Itoa(seconds) + "s"
	}
	return strconv.Itoa(seconds) + "s"
}

// HandleGetRecentSolves 获取最近解题记录（用于大屏实时流水）
func HandleGetRecentSolves(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// 获取比赛的血奖励配置和模式
	var firstBonus, secondBonus, thirdBonus int
	var contestMode string
	db.QueryRow(`SELECT COALESCE(mode, 'jeopardy'), COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).
		Scan(&contestMode, &firstBonus, &secondBonus, &thirdBonus)

	// 获取每道题的当前解题人数（根据比赛模式选择表）
	challengeSolveCountMap := make(map[int64]int)
	var countSQL string
	if contestMode == "awd-f" {
		countSQL = `SELECT challenge_id, COUNT(*) FROM team_solves_awdf WHERE contest_id = $1 GROUP BY challenge_id`
	} else {
		countSQL = `SELECT challenge_id, COUNT(*) FROM team_solves WHERE contest_id = $1 GROUP BY challenge_id`
	}
	countRows, _ := db.Query(countSQL, contestID)
	if countRows != nil {
		for countRows.Next() {
			var cid int64
			var cnt int
			countRows.Scan(&cid, &cnt)
			challengeSolveCountMap[cid] = cnt
		}
		countRows.Close()
	}

	// 获取每道题的配置（根据比赛模式选择表）
	challengeConfigMap := make(map[int64]struct{ InitialScore, MinScore, Difficulty int })
	var configSQL string
	if contestMode == "awd-f" {
		configSQL = `SELECT id, initial_score, min_score FROM contest_challenges_awdf WHERE contest_id = $1`
	} else {
		configSQL = `SELECT id, initial_score, min_score, difficulty FROM contest_challenges WHERE contest_id = $1`
	}
	configRows, _ := db.Query(configSQL, contestID)
	if configRows != nil {
		for configRows.Next() {
			var cid int64
			var initial, min, diff int
			if contestMode == "awd-f" {
				configRows.Scan(&cid, &initial, &min)
				diff = 5 // AWD-F 默认难度系数
			} else {
				configRows.Scan(&cid, &initial, &min, &diff)
			}
			challengeConfigMap[cid] = struct{ InitialScore, MinScore, Difficulty int }{initial, min, diff}
		}
		configRows.Close()
	}

	// 查询最近的解题记录（包括一二三血标记）（根据比赛模式选择表）
	var recentSolvesSQL string
	if contestMode == "awd-f" {
		recentSolvesSQL = `
			WITH ranked_solves AS (
				SELECT 
					ts.team_id, t.name as team_name, 
					ts.challenge_id, cc.id as contest_challenge_id, q.title as challenge_name,
					ts.solve_order, ts.solved_at,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves_awdf ts
				JOIN teams t ON ts.team_id = t.id
				JOIN contest_challenges_awdf cc ON ts.challenge_id = cc.id
				JOIN question_bank_awdf q ON cc.question_id = q.id
				WHERE ts.contest_id = $1
			)
			SELECT team_id, team_name, contest_challenge_id, challenge_name, solve_order, solved_at, blood_rank
			FROM ranked_solves
			ORDER BY solved_at DESC
			LIMIT $2`
	} else {
		recentSolvesSQL = `
			WITH ranked_solves AS (
				SELECT 
					ts.team_id, t.name as team_name, 
					ts.challenge_id, cc.id as contest_challenge_id, q.title as challenge_name,
					ts.solve_order, ts.solved_at,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves ts
				JOIN teams t ON ts.team_id = t.id
				JOIN contest_challenges cc ON ts.challenge_id = cc.id
				JOIN question_bank q ON cc.question_id = q.id
				WHERE ts.contest_id = $1
			)
			SELECT team_id, team_name, contest_challenge_id, challenge_name, solve_order, solved_at, blood_rank
			FROM ranked_solves
			ORDER BY solved_at DESC
			LIMIT $2`
	}
	rows, err := db.Query(recentSolvesSQL, contestID, limit)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"solves": []interface{}{}})
		return
	}
	defer rows.Close()

	type SolveRecord struct {
		TeamID        int64  `json:"teamId"`
		TeamName      string `json:"teamName"`
		ChallengeID   int64  `json:"challengeId"`
		ChallengeName string `json:"challengeName"`
		Score         int    `json:"score"`
		SolvedAt      string `json:"solvedAt"`
		BloodRank     int    `json:"bloodRank"` // 1=一血, 2=二血, 3=三血, >3=普通
	}

	var solves []SolveRecord
	for rows.Next() {
		var s SolveRecord
		var challengeID int64
		var solveOrder int
		var solvedAt time.Time
		if err := rows.Scan(&s.TeamID, &s.TeamName, &challengeID, &s.ChallengeName, &solveOrder, &solvedAt, &s.BloodRank); err != nil {
			continue
		}
		s.ChallengeID = challengeID
		s.SolvedAt = solvedAt.Format("2006-01-02 15:04:05")
		// 动态计算分数
		config := challengeConfigMap[challengeID]
		solveCount := challengeSolveCountMap[challengeID]
		baseScore := calculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
		s.Score = calculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)
		solves = append(solves, s)
	}

	if solves == nil {
		solves = []SolveRecord{}
	}

	c.JSON(http.StatusOK, gin.H{"solves": solves})
}

// HandleGetMonitorData 获取大屏综合数据（排行榜+趋势+解题流水）
func HandleGetMonitorData(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 1. 获取比赛信息
	var contestName string
	var startTime, endTime time.Time
	var status, contestMode string
	var firstBonus, secondBonus, thirdBonus int
	err := db.QueryRow(`SELECT name, start_time, end_time, status, COALESCE(mode, 'jeopardy'), COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).
		Scan(&contestName, &startTime, &endTime, &status, &contestMode, &firstBonus, &secondBonus, &thirdBonus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CONTEST_NOT_FOUND"})
		return
	}
	// 将时间转为本地时区（数据库timestamp without time zone被解析为UTC，需要当作本地时间处理）
	loc, _ := time.LoadLocation("Asia/Shanghai")
	startTime = time.Date(startTime.Year(), startTime.Month(), startTime.Day(), startTime.Hour(), startTime.Minute(), startTime.Second(), startTime.Nanosecond(), loc)
	endTime = time.Date(endTime.Year(), endTime.Month(), endTime.Day(), endTime.Hour(), endTime.Minute(), endTime.Second(), endTime.Nanosecond(), loc)

	// 获取每道题的当前解题人数（根据比赛模式选择表）
	challengeSolveCountMap := make(map[int64]int)
	var countSQLForMonitor string
	if contestMode == "awd-f" {
		countSQLForMonitor = `SELECT challenge_id, COUNT(*) FROM team_solves_awdf WHERE contest_id = $1 GROUP BY challenge_id`
	} else {
		countSQLForMonitor = `SELECT challenge_id, COUNT(*) FROM team_solves WHERE contest_id = $1 GROUP BY challenge_id`
	}
	countRows, _ := db.Query(countSQLForMonitor, contestID)
	if countRows != nil {
		for countRows.Next() {
			var cid int64
			var cnt int
			countRows.Scan(&cid, &cnt)
			challengeSolveCountMap[cid] = cnt
		}
		countRows.Close()
	}

	// 获取每道题的配置（根据比赛模式选择表）
	challengeConfigMap := make(map[int64]struct{ InitialScore, MinScore, Difficulty int })
	var configSQL string
	if contestMode == "awd-f" {
		// AWD-F 表没有 difficulty 字段，使用默认值 5
		configSQL = `SELECT id, initial_score, min_score FROM contest_challenges_awdf WHERE contest_id = $1`
	} else {
		configSQL = `SELECT id, initial_score, min_score, difficulty FROM contest_challenges WHERE contest_id = $1`
	}
	configRows, _ := db.Query(configSQL, contestID)
	if configRows != nil {
		for configRows.Next() {
			var cid int64
			var initial, min, diff int
			if contestMode == "awd-f" {
				configRows.Scan(&cid, &initial, &min)
				diff = 5 // AWD-F 默认难度系数
			} else {
				configRows.Scan(&cid, &initial, &min, &diff)
			}
			challengeConfigMap[cid] = struct{ InitialScore, MinScore, Difficulty int }{initial, min, diff}
		}
		configRows.Close()
	}

	// 2. 获取所有已审核通过队伍的解题记录，动态计算分数（根据比赛模式选择表）
	var rankSQL string
	if contestMode == "awd-f" {
		rankSQL = `
			SELECT ct.team_id, t.name, t.avatar, t.captain_id, u.avatar as captain_avatar, ts.challenge_id, ts.solve_order, ts.solved_at
			FROM contest_teams ct
			JOIN teams t ON ct.team_id = t.id
			LEFT JOIN users u ON t.captain_id = u.id
			LEFT JOIN team_solves_awdf ts ON ct.team_id = ts.team_id AND ts.contest_id = $1
			WHERE ct.contest_id = $1 AND ct.status IN ('approved', 'pending')
			ORDER BY ct.team_id, ts.solved_at`
	} else {
		rankSQL = `
			SELECT ct.team_id, t.name, t.avatar, t.captain_id, u.avatar as captain_avatar, ts.challenge_id, ts.solve_order, ts.solved_at
			FROM contest_teams ct
			JOIN teams t ON ct.team_id = t.id
			LEFT JOIN users u ON t.captain_id = u.id
			LEFT JOIN team_solves ts ON ct.team_id = ts.team_id AND ts.contest_id = $1
			WHERE ct.contest_id = $1 AND ct.status IN ('approved', 'pending')
			ORDER BY ct.team_id, ts.solved_at`
	}
	rankRows, err := db.Query(rankSQL, contestID)

	type TeamScore struct {
		Rank         int    `json:"rank"`
		TeamID       int64  `json:"teamId"`
		TeamName     string `json:"teamName"`
		Avatar       string `json:"avatar"`
		TotalScore   int    `json:"totalScore"`
		AttackScore  int    `json:"attackScore"`
		DefenseScore int    `json:"defenseScore"`
		SolveCount   int    `json:"solveCount"`
	}

	teamScoreMap := make(map[int64]*TeamScore)
	teamLastSolveMap := make(map[int64]time.Time)

	if err == nil {
		defer rankRows.Close()
		for rankRows.Next() {
			var teamID int64
			var teamName string
			var teamAvatar, captainAvatar sql.NullString
			var captainID sql.NullInt64
			var challengeID sql.NullInt64
			var solveOrder sql.NullInt64
			var solvedAt sql.NullTime

			if err := rankRows.Scan(&teamID, &teamName, &teamAvatar, &captainID, &captainAvatar, &challengeID, &solveOrder, &solvedAt); err != nil {
				continue
			}

			// 初始化队伍数据
			if _, exists := teamScoreMap[teamID]; !exists {
				// 队伍头像优先，如为空则使用队长头像
				avatarStr := ""
				if teamAvatar.Valid && teamAvatar.String != "" {
					avatarStr = teamAvatar.String
				} else if captainAvatar.Valid && captainAvatar.String != "" {
					avatarStr = captainAvatar.String
				}
				teamScoreMap[teamID] = &TeamScore{
					TeamID:   teamID,
					TeamName: teamName,
					Avatar:   avatarStr,
				}
			}

			// 如果有解题记录，计算攻击分数
			if challengeID.Valid && solveOrder.Valid {
				config := challengeConfigMap[challengeID.Int64]
				solveCount := challengeSolveCountMap[challengeID.Int64]
				baseScore := calculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
				score := calculateScoreWithBonus(baseScore, int(solveOrder.Int64), firstBonus, secondBonus, thirdBonus)

				teamScoreMap[teamID].AttackScore += score
				teamScoreMap[teamID].TotalScore += score
				teamScoreMap[teamID].SolveCount++

				if solvedAt.Valid && solvedAt.Time.After(teamLastSolveMap[teamID]) {
					teamLastSolveMap[teamID] = solvedAt.Time
				}
			}
		}
	}

	// AWD-F 模式：计算防守分数
	if contestMode == "awd-f" {
		defenseRows, err := db.Query(`
			SELECT team_id, SUM(COALESCE(score_earned, 0)) as total_defense
			FROM awdf_exp_results
			WHERE contest_id = $1 AND defense_success = true
			GROUP BY team_id`, contestID)
		if err == nil {
			defer defenseRows.Close()
			for defenseRows.Next() {
				var teamID int64
				var defenseScore int
				if err := defenseRows.Scan(&teamID, &defenseScore); err != nil {
					continue
				}
				if ts, exists := teamScoreMap[teamID]; exists {
					ts.DefenseScore = defenseScore
					ts.TotalScore += defenseScore
				}
			}
		}
	}

	// 转换为数组并排序
	var rankings []TeamScore
	for teamID, ts := range teamScoreMap {
		_ = teamID
		rankings = append(rankings, *ts)
	}

	// 按总分降序、最后解题时间升序排序
	sort.Slice(rankings, func(i, j int) bool {
		if rankings[i].TotalScore != rankings[j].TotalScore {
			return rankings[i].TotalScore > rankings[j].TotalScore
		}
		lastI := teamLastSolveMap[int64(rankings[i].TeamID)]
		lastJ := teamLastSolveMap[int64(rankings[j].TeamID)]
		if !lastI.IsZero() && !lastJ.IsZero() {
			return lastI.Before(lastJ)
		}
		return rankings[i].TeamName < rankings[j].TeamName
	})

	for i := range rankings {
		rankings[i].Rank = i + 1
	}

	if rankings == nil {
		rankings = []TeamScore{}
	}

	// 3. 获取解题记录（优先包含一二三血，再取最近记录，去重后最多50条）
	var solvesSQL string
	if contestMode == "awd-f" {
		solvesSQL = `
			WITH ranked_solves AS (
				SELECT 
					ts.team_id, t.name as team_name, 
					ts.challenge_id, q.title as challenge_name,
					ts.solve_order, ts.solved_at,
					cfv.first_viewed_at,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves_awdf ts
				JOIN teams t ON ts.team_id = t.id
				JOIN contest_challenges_awdf cc ON ts.challenge_id = cc.id
				JOIN question_bank_awdf q ON cc.question_id = q.id
				LEFT JOIN challenge_first_views cfv ON cfv.contest_id = ts.contest_id 
					AND cfv.challenge_id = ts.challenge_id AND cfv.team_id = ts.team_id
				WHERE ts.contest_id = $1
			),
			bloods AS (
				SELECT * FROM ranked_solves WHERE blood_rank <= 3
			),
			recent AS (
				SELECT * FROM ranked_solves ORDER BY solved_at DESC LIMIT 50
			),
			combined AS (
				SELECT * FROM bloods
				UNION
				SELECT * FROM recent
			)
			SELECT team_id, team_name, challenge_id, challenge_name, solve_order, solved_at, first_viewed_at, blood_rank
			FROM combined
			ORDER BY solved_at DESC`
	} else {
		solvesSQL = `
			WITH ranked_solves AS (
				SELECT 
					ts.team_id, t.name as team_name, 
					ts.challenge_id, q.title as challenge_name,
					ts.solve_order, ts.solved_at,
					cfv.first_viewed_at,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves ts
				JOIN teams t ON ts.team_id = t.id
				JOIN contest_challenges cc ON ts.challenge_id = cc.id
				JOIN question_bank q ON cc.question_id = q.id
				LEFT JOIN challenge_first_views cfv ON cfv.contest_id = ts.contest_id 
					AND cfv.challenge_id = ts.challenge_id AND cfv.team_id = ts.team_id
				WHERE ts.contest_id = $1
			),
			bloods AS (
				SELECT * FROM ranked_solves WHERE blood_rank <= 3
			),
			recent AS (
				SELECT * FROM ranked_solves ORDER BY solved_at DESC LIMIT 50
			),
			combined AS (
				SELECT * FROM bloods
				UNION
				SELECT * FROM recent
			)
			SELECT team_id, team_name, challenge_id, challenge_name, solve_order, solved_at, first_viewed_at, blood_rank
			FROM combined
			ORDER BY solved_at DESC`
	}
	solveRows, err := db.Query(solvesSQL, contestID)

	type SolveRecord struct {
		TeamID        int64  `json:"teamId"`
		TeamName      string `json:"teamName"`
		ChallengeID   int64  `json:"challengeId"`
		ChallengeName string `json:"challengeName"`
		Score         int    `json:"score"`
		SolvedAt      string `json:"solvedAt"`
		BloodRank     int    `json:"bloodRank"`
		SolveTime     string `json:"solveTime"`
	}

	var solves []SolveRecord
	if err == nil {
		defer solveRows.Close()
		for solveRows.Next() {
			var s SolveRecord
			var challengeID int64
			var solveOrder int
			var solvedAt time.Time
			var firstViewedAt sql.NullTime
			if err := solveRows.Scan(&s.TeamID, &s.TeamName, &challengeID, &s.ChallengeName, &solveOrder, &solvedAt, &firstViewedAt, &s.BloodRank); err != nil {
				continue
			}
			s.ChallengeID = challengeID
			s.SolvedAt = solvedAt.Format("2006-01-02 15:04:05")
			// 动态计算分数
			config := challengeConfigMap[challengeID]
			solveCount := challengeSolveCountMap[challengeID]
			baseScore := calculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
			s.Score = calculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)
			// 计算解题用时
			if firstViewedAt.Valid {
				duration := solvedAt.Sub(firstViewedAt.Time)
				s.SolveTime = formatDuration(duration)
			}
			solves = append(solves, s)
		}
	}
	if solves == nil {
		solves = []SolveRecord{}
	}

	// 4. 计算剩余时间（开始倒计时 / 结束倒计时）
	var startRemainingSeconds int64 = 0
	var endRemainingSeconds int64 = 0
	now := time.Now().In(loc) // 使用同一时区
	
	if status == "pending" {
		// 比赛未开始，计算距离开始的时间
		startRemainingSeconds = int64(startTime.Sub(now).Seconds())
		if startRemainingSeconds < 0 {
			startRemainingSeconds = 0
		}
	} else if status == "running" {
		// 比赛进行中，计算距离结束的时间
		endRemainingSeconds = int64(endTime.Sub(now).Seconds())
		if endRemainingSeconds < 0 {
			endRemainingSeconds = 0
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"contest": gin.H{
			"id":                    contestID,
			"name":                  contestName,
			"status":                status,
			"mode":                  contestMode,
			"startTime":             startTime.Format("2006-01-02 15:04:05"),
			"endTime":               endTime.Format("2006-01-02 15:04:05"),
			"startRemainingSeconds": startRemainingSeconds,
			"endRemainingSeconds":   endRemainingSeconds,
		},
		"rankings": rankings,
		"solves":   solves,
		"events":   GetMonitorEventsFromDB(db, contestID),
		"trend":    getScoreTrendData(db, contestID, contestMode, challengeConfigMap, challengeSolveCountMap, firstBonus, secondBonus, thirdBonus),
	})
}

// 动态分数计算函数 - 使用指数衰减公式
// S(N) = Smin + (Smax - Smin) × e^(-(N-1)/(10D))
func calculateDynamicScore(initialScore, minScore, difficulty, solveCount int) int {
	// 如果还没人解出，返回原始分值
	if solveCount == 0 {
		return initialScore
	}
	// 确保难度系数在有效范围
	if difficulty < 1 {
		difficulty = 1
	} else if difficulty > 10 {
		difficulty = 10
	}
	// 指数衰减公式
	decayFactor := math.Exp(-float64(solveCount-1) / (float64(difficulty) * 10.0))
	score := float64(minScore) + float64(initialScore-minScore)*decayFactor
	// 确保不低于最低分
	if score < float64(minScore) {
		return minScore
	}
	return int(math.Round(score))
}

func calculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus int) int {
	bonusPercent := 0
	if solveOrder == 1 {
		bonusPercent = firstBonus
	} else if solveOrder == 2 {
		bonusPercent = secondBonus
	} else if solveOrder == 3 {
		bonusPercent = thirdBonus
	}
	return baseScore + (baseScore * bonusPercent / 100)
}

// GetMonitorDataForBroadcast 获取广播数据（内部调用）
func GetMonitorDataForBroadcast(db *sql.DB, contestID string) map[string]interface{} {
	// 获取比赛的血奖励配置和模式
	var firstBonus, secondBonus, thirdBonus int
	var contestMode string
	db.QueryRow(`SELECT COALESCE(mode, 'jeopardy'), COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).
		Scan(&contestMode, &firstBonus, &secondBonus, &thirdBonus)

	// 获取每道题的当前解题人数（根据比赛模式选择表）
	challengeSolveCountMap := make(map[int64]int)
	var countSQLBroadcast string
	if contestMode == "awd-f" {
		countSQLBroadcast = `SELECT challenge_id, COUNT(*) FROM team_solves_awdf WHERE contest_id = $1 GROUP BY challenge_id`
	} else {
		countSQLBroadcast = `SELECT challenge_id, COUNT(*) FROM team_solves WHERE contest_id = $1 GROUP BY challenge_id`
	}
	countRows, _ := db.Query(countSQLBroadcast, contestID)
	if countRows != nil {
		for countRows.Next() {
			var cid int64
			var cnt int
			countRows.Scan(&cid, &cnt)
			challengeSolveCountMap[cid] = cnt
		}
		countRows.Close()
	}

	// 获取每道题的配置（根据比赛模式选择表）
	challengeConfigMap := make(map[int64]struct{ InitialScore, MinScore, Difficulty int })
	var configSQL string
	if contestMode == "awd-f" {
		// AWD-F 表没有 difficulty 字段，使用默认值 5
		configSQL = `SELECT id, initial_score, min_score FROM contest_challenges_awdf WHERE contest_id = $1`
	} else {
		configSQL = `SELECT id, initial_score, min_score, difficulty FROM contest_challenges WHERE contest_id = $1`
	}
	configRows, _ := db.Query(configSQL, contestID)
	if configRows != nil {
		for configRows.Next() {
			var cid int64
			var initial, min, diff int
			if contestMode == "awd-f" {
				configRows.Scan(&cid, &initial, &min)
				diff = 5 // AWD-F 默认难度系数
			} else {
				configRows.Scan(&cid, &initial, &min, &diff)
			}
			challengeConfigMap[cid] = struct{ InitialScore, MinScore, Difficulty int }{initial, min, diff}
		}
		configRows.Close()
	}

	// 获取排行榜 - 动态计算分数（根据比赛模式选择表）
	var rankSQLBroadcast string
	if contestMode == "awd-f" {
		rankSQLBroadcast = `
			SELECT ct.team_id, t.name, t.avatar, t.captain_id, u.avatar as captain_avatar, ts.challenge_id, ts.solve_order, ts.solved_at
			FROM contest_teams ct
			JOIN teams t ON ct.team_id = t.id
			LEFT JOIN users u ON t.captain_id = u.id
			LEFT JOIN team_solves_awdf ts ON ct.team_id = ts.team_id AND ts.contest_id = $1
			WHERE ct.contest_id = $1 AND ct.status IN ('approved', 'pending')
			ORDER BY ct.team_id, ts.solved_at`
	} else {
		rankSQLBroadcast = `
			SELECT ct.team_id, t.name, t.avatar, t.captain_id, u.avatar as captain_avatar, ts.challenge_id, ts.solve_order, ts.solved_at
			FROM contest_teams ct
			JOIN teams t ON ct.team_id = t.id
			LEFT JOIN users u ON t.captain_id = u.id
			LEFT JOIN team_solves ts ON ct.team_id = ts.team_id AND ts.contest_id = $1
			WHERE ct.contest_id = $1 AND ct.status IN ('approved', 'pending')
			ORDER BY ct.team_id, ts.solved_at`
	}
	rankRows, err := db.Query(rankSQLBroadcast, contestID)

	type TeamScore struct {
		Rank         int    `json:"rank"`
		TeamID       int64  `json:"teamId"`
		TeamName     string `json:"teamName"`
		Avatar       string `json:"avatar"`
		TotalScore   int    `json:"totalScore"`
		AttackScore  int    `json:"attackScore"`
		DefenseScore int    `json:"defenseScore"`
		SolveCount   int    `json:"solveCount"`
	}

	teamScoreMap := make(map[int64]*TeamScore)
	teamLastSolveMap := make(map[int64]time.Time)

	if err == nil {
		defer rankRows.Close()
		for rankRows.Next() {
			var teamID int64
			var teamName string
			var teamAvatar, captainAvatar sql.NullString
			var captainID sql.NullInt64
			var challengeID sql.NullInt64
			var solveOrder sql.NullInt64
			var solvedAt sql.NullTime

			if err := rankRows.Scan(&teamID, &teamName, &teamAvatar, &captainID, &captainAvatar, &challengeID, &solveOrder, &solvedAt); err != nil {
				continue
			}

			if _, exists := teamScoreMap[teamID]; !exists {
				// 队伍头像优先，如为空则使用队长头像
				avatarStr := ""
				if teamAvatar.Valid && teamAvatar.String != "" {
					avatarStr = teamAvatar.String
				} else if captainAvatar.Valid && captainAvatar.String != "" {
					avatarStr = captainAvatar.String
				}
				teamScoreMap[teamID] = &TeamScore{
					TeamID:   teamID,
					TeamName: teamName,
					Avatar:   avatarStr,
				}
			}

			if challengeID.Valid && solveOrder.Valid {
				config := challengeConfigMap[challengeID.Int64]
				solveCount := challengeSolveCountMap[challengeID.Int64]
				baseScore := calculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
				score := calculateScoreWithBonus(baseScore, int(solveOrder.Int64), firstBonus, secondBonus, thirdBonus)

				teamScoreMap[teamID].AttackScore += score
				teamScoreMap[teamID].TotalScore += score
				teamScoreMap[teamID].SolveCount++

				if solvedAt.Valid && solvedAt.Time.After(teamLastSolveMap[teamID]) {
					teamLastSolveMap[teamID] = solvedAt.Time
				}
			}
		}
	}

	// AWD-F 模式：计算防守分数
	if contestMode == "awd-f" {
		defenseRows, err := db.Query(`
			SELECT team_id, SUM(COALESCE(score_earned, 0)) as total_defense
			FROM awdf_exp_results
			WHERE contest_id = $1 AND defense_success = true
			GROUP BY team_id`, contestID)
		if err == nil {
			defer defenseRows.Close()
			for defenseRows.Next() {
				var teamID int64
				var defenseScore int
				if err := defenseRows.Scan(&teamID, &defenseScore); err != nil {
					continue
				}
				if ts, exists := teamScoreMap[teamID]; exists {
					ts.DefenseScore = defenseScore
					ts.TotalScore += defenseScore
				}
			}
		}
	}

	var rankings []TeamScore
	for _, ts := range teamScoreMap {
		rankings = append(rankings, *ts)
	}

	sort.Slice(rankings, func(i, j int) bool {
		if rankings[i].TotalScore != rankings[j].TotalScore {
			return rankings[i].TotalScore > rankings[j].TotalScore
		}
		lastI := teamLastSolveMap[int64(rankings[i].TeamID)]
		lastJ := teamLastSolveMap[int64(rankings[j].TeamID)]
		if !lastI.IsZero() && !lastJ.IsZero() {
			return lastI.Before(lastJ)
		}
		return rankings[i].TeamName < rankings[j].TeamName
	})

	for i := range rankings {
		rankings[i].Rank = i + 1
	}

	if rankings == nil {
		rankings = []TeamScore{}
	}

	// 获取解题记录（优先包含一二三血，再取最近50条，去重）
	var solvesSQL string
	if contestMode == "awd-f" {
		solvesSQL = `
			WITH ranked_solves AS (
				SELECT 
					ts.team_id, t.name as team_name, 
					ts.challenge_id, q.title as challenge_name,
					ts.solve_order, ts.solved_at,
					cfv.first_viewed_at,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves_awdf ts
				JOIN teams t ON ts.team_id = t.id
				JOIN contest_challenges_awdf cc ON ts.challenge_id = cc.id
				JOIN question_bank_awdf q ON cc.question_id = q.id
				LEFT JOIN challenge_first_views cfv ON cfv.contest_id = ts.contest_id 
					AND cfv.challenge_id = ts.challenge_id AND cfv.team_id = ts.team_id
				WHERE ts.contest_id = $1
			),
			bloods AS (
				SELECT * FROM ranked_solves WHERE blood_rank <= 3
			),
			recent AS (
				SELECT * FROM ranked_solves ORDER BY solved_at DESC LIMIT 50
			),
			combined AS (
				SELECT * FROM bloods
				UNION
				SELECT * FROM recent
			)
			SELECT team_id, team_name, challenge_id, challenge_name, solve_order, solved_at, first_viewed_at, blood_rank
			FROM combined
			ORDER BY solved_at DESC`
	} else {
		solvesSQL = `
			WITH ranked_solves AS (
				SELECT 
					ts.team_id, t.name as team_name, 
					ts.challenge_id, q.title as challenge_name,
					ts.solve_order, ts.solved_at,
					cfv.first_viewed_at,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves ts
				JOIN teams t ON ts.team_id = t.id
				JOIN contest_challenges cc ON ts.challenge_id = cc.id
				JOIN question_bank q ON cc.question_id = q.id
				LEFT JOIN challenge_first_views cfv ON cfv.contest_id = ts.contest_id 
					AND cfv.challenge_id = ts.challenge_id AND cfv.team_id = ts.team_id
				WHERE ts.contest_id = $1
			),
			bloods AS (
				SELECT * FROM ranked_solves WHERE blood_rank <= 3
			),
			recent AS (
				SELECT * FROM ranked_solves ORDER BY solved_at DESC LIMIT 50
			),
			combined AS (
				SELECT * FROM bloods
				UNION
				SELECT * FROM recent
			)
			SELECT team_id, team_name, challenge_id, challenge_name, solve_order, solved_at, first_viewed_at, blood_rank
			FROM combined
			ORDER BY solved_at DESC`
	}
	solveRows, _ := db.Query(solvesSQL, contestID)

	type SolveRecord struct {
		TeamID        int64  `json:"teamId"`
		TeamName      string `json:"teamName"`
		ChallengeID   int64  `json:"challengeId"`
		ChallengeName string `json:"challengeName"`
		Score         int    `json:"score"`
		SolvedAt      string `json:"solvedAt"`
		BloodRank     int    `json:"bloodRank"`
		SolveTime     string `json:"solveTime"`
	}

	var solves []SolveRecord
	if solveRows != nil {
		defer solveRows.Close()
		for solveRows.Next() {
			var s SolveRecord
			var challengeID int64
			var solveOrder int
			var solvedAt time.Time
			var firstViewedAt sql.NullTime
			if err := solveRows.Scan(&s.TeamID, &s.TeamName, &challengeID, &s.ChallengeName, &solveOrder, &solvedAt, &firstViewedAt, &s.BloodRank); err != nil {
				continue
			}
			s.ChallengeID = challengeID
			s.SolvedAt = solvedAt.Format("2006-01-02 15:04:05")
			// 动态计算分数
			config := challengeConfigMap[challengeID]
			solveCount := challengeSolveCountMap[challengeID]
			baseScore := calculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
			s.Score = calculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)
			if firstViewedAt.Valid {
				duration := solvedAt.Sub(firstViewedAt.Time)
				s.SolveTime = formatDuration(duration)
			}
			solves = append(solves, s)
		}
	}
	if solves == nil {
		solves = []SolveRecord{}
	}

	return map[string]interface{}{
		"type":     "update",
		"rankings": rankings,
		"solves":   solves,
		"events":   GetMonitorEventsFromDB(db, contestID),
		"trend":    getScoreTrendData(db, contestID, contestMode, challengeConfigMap, challengeSolveCountMap, firstBonus, secondBonus, thirdBonus),
	}
}

// getScoreTrendData 获取分数趋势数据（内部使用，避免循环导入）
func getScoreTrendData(db *sql.DB, contestID string, contestMode string, challengeConfigMap map[int64]struct{ InitialScore, MinScore, Difficulty int }, challengeSolveCountMap map[int64]int, firstBonus, secondBonus, thirdBonus int) map[string]interface{} {
	// 查询前5名队伍的解题记录（根据比赛模式选择表）
	var trendSQL string
	if contestMode == "awd-f" {
		trendSQL = `
			WITH team_scores AS (
				SELECT ts.team_id, t.name, COUNT(*) as solve_count
				FROM team_solves_awdf ts
				JOIN teams t ON ts.team_id = t.id
				WHERE ts.contest_id = $1
				GROUP BY ts.team_id, t.name
				ORDER BY solve_count DESC
				LIMIT 5
			)
			SELECT ts2.team_id, team_scores.name, ts2.challenge_id, ts2.solve_order, ts2.solved_at
			FROM team_scores
			JOIN team_solves_awdf ts2 ON team_scores.team_id = ts2.team_id AND ts2.contest_id = $1
			ORDER BY ts2.solved_at ASC`
	} else {
		trendSQL = `
			WITH team_scores AS (
				SELECT ts.team_id, t.name, COUNT(*) as solve_count
				FROM team_solves ts
				JOIN teams t ON ts.team_id = t.id
				WHERE ts.contest_id = $1
				GROUP BY ts.team_id, t.name
				ORDER BY solve_count DESC
				LIMIT 5
			)
			SELECT ts2.team_id, team_scores.name, ts2.challenge_id, ts2.solve_order, ts2.solved_at
			FROM team_scores
			JOIN team_solves ts2 ON team_scores.team_id = ts2.team_id AND ts2.contest_id = $1
			ORDER BY ts2.solved_at ASC`
	}
	rows, err := db.Query(trendSQL, contestID)
	if err != nil {
		return map[string]interface{}{"labels": []string{}, "teams": []interface{}{}}
	}
	defer rows.Close()

	type SolveRecord struct {
		ChallengeID int64
		SolveOrder  int
		SolvedAt    time.Time
	}
	teamData := make(map[int64]struct {
		Name   string
		Solves []SolveRecord
	})
	teamOrder := []int64{}
	allTimes := []time.Time{}
	timeSet := make(map[int64]bool)

	for rows.Next() {
		var teamID int64
		var name string
		var challengeID int64
		var solveOrder int
		var solvedAt time.Time
		if err := rows.Scan(&teamID, &name, &challengeID, &solveOrder, &solvedAt); err != nil {
			continue
		}

		if _, exists := teamData[teamID]; !exists {
			teamData[teamID] = struct {
				Name   string
				Solves []SolveRecord
			}{Name: name, Solves: []SolveRecord{}}
			teamOrder = append(teamOrder, teamID)
		}
		d := teamData[teamID]
		d.Solves = append(d.Solves, SolveRecord{ChallengeID: challengeID, SolveOrder: solveOrder, SolvedAt: solvedAt})
		teamData[teamID] = d

		unixTime := solvedAt.Unix()
		if !timeSet[unixTime] {
			timeSet[unixTime] = true
			allTimes = append(allTimes, solvedAt)
		}
	}

	if len(teamData) == 0 {
		return map[string]interface{}{"labels": []string{}, "teams": []interface{}{}}
	}

	sort.Slice(allTimes, func(i, j int) bool {
		return allTimes[i].Before(allTimes[j])
	})

	labels := []string{"Start"}
	timestamps := []time.Time{allTimes[0].Add(-time.Second)}
	for _, t := range allTimes {
		labels = append(labels, t.Format("15:04"))
		timestamps = append(timestamps, t)
	}

	type TeamTrend struct {
		Name   string `json:"name"`
		Scores []int  `json:"scores"`
	}
	var teamTrends []TeamTrend

	for _, teamID := range teamOrder {
		data := teamData[teamID]
		var scores []int
		for _, ts := range timestamps {
			cumScore := 0
			for _, solve := range data.Solves {
				if !solve.SolvedAt.After(ts) {
					// 动态计算分数
					config := challengeConfigMap[solve.ChallengeID]
					solveCount := challengeSolveCountMap[solve.ChallengeID]
					baseScore := calculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
					score := calculateScoreWithBonus(baseScore, solve.SolveOrder, firstBonus, secondBonus, thirdBonus)
					cumScore += score
				}
			}
			scores = append(scores, cumScore)
		}
		teamTrends = append(teamTrends, TeamTrend{Name: data.Name, Scores: scores})
	}

	return map[string]interface{}{
		"labels": labels,
		"teams":  teamTrends,
	}
}
