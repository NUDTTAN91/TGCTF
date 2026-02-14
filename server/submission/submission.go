// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package submission

import (
	"database/sql"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"tgctf/server/admin"
	"tgctf/server/logs"
	"tgctf/server/monitor"
)

// SubmitFlagRequest 提交flag请求
type SubmitFlagRequest struct {
	Flag string `json:"flag" binding:"required"`
}

// SubmitFlagResponse 提交flag响应
type SubmitFlagResponse struct {
	Correct     bool   `json:"correct"`
	Message     string `json:"message"`
	Score       int    `json:"score,omitempty"`
	FirstBlood  bool   `json:"firstBlood,omitempty"`
	SecondBlood bool   `json:"secondBlood,omitempty"`
	ThirdBlood  bool   `json:"thirdBlood,omitempty"`
}

// 公告函数类型定义
type AnnounceCheatingFunc func(db *sql.DB, contestID int64, teamName, reason string)
type AnnounceBloodFunc func(db *sql.DB, contestID int64, challengeName, teamName string, bloodType int)

// 全局变量，用于注入公告函数
var AnnounceCheating AnnounceCheatingFunc
var AnnounceBlood AnnounceBloodFunc

// HandleSubmitFlag 提交flag
func HandleSubmitFlag(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Param("challengeId")
	userID := c.GetInt64("userID")

	var req SubmitFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "请输入flag"})
		return
	}

	var teamID sql.NullInt64
	err := db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if err != nil || !teamID.Valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_TEAM", "message": "您还未加入队伍"})
		return
	}

	// 错误提交冷却检查（10秒）- 使用数据库时间计算避免时区问题
	var elapsedSeconds float64
	err = db.QueryRow(`SELECT EXTRACT(EPOCH FROM (NOW() - submitted_at)) FROM submissions 
		WHERE team_id = $1 AND contest_id = $2 AND is_correct = false 
		ORDER BY submitted_at DESC LIMIT 1`,
		teamID.Int64, contestID).Scan(&elapsedSeconds)
	if err == nil {
		cooldown := 10.0 // 10秒冷却
		if elapsedSeconds < cooldown {
			retryAfter := int(math.Ceil(cooldown - elapsedSeconds))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "TOO_FAST",
				"message":    "提交太频繁，请稍后再试",
				"retryAfter": retryAfter,
			})
			return
		}
	}

	var contestStatus, contestMode string
	err = db.QueryRow(`SELECT status, mode FROM contests WHERE id = $1`, contestID).Scan(&contestStatus, &contestMode)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "CONTEST_NOT_FOUND", "message": "比赛不存在"})
		return
	}
	if contestStatus != "running" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CONTEST_NOT_RUNNING", "message": "比赛未在进行中"})
		return
	}

	var challengeStatus string
	var initialScore, minScore int
	var questionID int64
	if contestMode == "awd-f" {
		err = db.QueryRow(`SELECT status, initial_score, min_score, question_id FROM contest_challenges_awdf WHERE id = $1 AND contest_id = $2`,
			challengeID, contestID).Scan(&challengeStatus, &initialScore, &minScore, &questionID)
	} else {
		err = db.QueryRow(`SELECT status, initial_score, min_score, question_id FROM contest_challenges WHERE id = $1 AND contest_id = $2`,
			challengeID, contestID).Scan(&challengeStatus, &initialScore, &minScore, &questionID)
	}
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "CHALLENGE_NOT_FOUND", "message": "题目不存在"})
		return
	}
	if challengeStatus != "public" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CHALLENGE_NOT_PUBLIC", "message": "题目未开放"})
		return
	}

	// 检查是否已解题（AWD-F 和普通模式使用不同的表）
	var existingSolve int64
	if contestMode == "awd-f" {
		err = db.QueryRow(`SELECT id FROM team_solves_awdf WHERE contest_id = $1 AND challenge_id = $2 AND team_id = $3`,
			contestID, challengeID, teamID.Int64).Scan(&existingSolve)
	} else {
		err = db.QueryRow(`SELECT id FROM team_solves WHERE contest_id = $1 AND challenge_id = $2 AND team_id = $3`,
			contestID, challengeID, teamID.Int64).Scan(&existingSolve)
	}
	if err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ALREADY_SOLVED", "message": "您的队伍已解出该题"})
		return
	}

	var flagType string
	var staticFlag sql.NullString
	if contestMode == "awd-f" {
		// AWD-F 模式默认使用动态 flag
		flagType = "dynamic"
	} else {
		db.QueryRow(`SELECT flag_type, flag FROM question_bank WHERE id = $1`, questionID).Scan(&flagType, &staticFlag)
	}

	submittedFlag := strings.TrimSpace(req.Flag)
	isCorrect := false
	cheatingDetected := false
	var cheatingVictimTeamID int64 = 0
	var cheatingVictimTeamName string

	if flagType == "dynamic" {
		var correctFlag string
		err = db.QueryRow(`SELECT flag FROM team_challenge_flags WHERE team_id = $1 AND challenge_id = $2`,
			teamID.Int64, challengeID).Scan(&correctFlag)
		if err == nil && submittedFlag == correctFlag {
			isCorrect = true
		} else {
			var otherTeamID int64
			err = db.QueryRow(`SELECT team_id FROM team_challenge_flags WHERE challenge_id = $1 AND flag = $2 AND team_id != $3`,
				challengeID, submittedFlag, teamID.Int64).Scan(&otherTeamID)
			if err == nil {
				cheatingDetected = true
				cheatingVictimTeamID = otherTeamID
				db.QueryRow(`SELECT name FROM teams WHERE id = $1`, otherTeamID).Scan(&cheatingVictimTeamName)
				log.Printf("[CHEATING DETECTED] Team %d submitted flag belonging to team %d for challenge %s",
					teamID.Int64, otherTeamID, challengeID)
			}
		}
	} else {
		if staticFlag.Valid && submittedFlag == staticFlag.String {
			isCorrect = true
		}
	}

	if cheatingDetected {
		contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
		challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
		var submitterTeamName string
		db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&submitterTeamName)

		// 记录作弊日志
		clientIP := c.ClientIP()
		logs.WriteLog(db, logs.TypeCheating, logs.LevelWarning, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
			"队伍 ["+submitterTeamName+"] 提交了其他队伍的Flag，涉嫌作弊", map[string]interface{}{
				"flag": submittedFlag, "victimTeam": cheatingVictimTeamName,
			})

		_, err = db.Exec(`INSERT INTO submissions (contest_id, challenge_id, team_id, user_id, flag, is_correct, is_cheating, cheating_victim_team_id, score, ip_address)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			contestID, challengeID, teamID.Int64, userID, submittedFlag, false, true, cheatingVictimTeamID, 0, clientIP)
		if err != nil {
			log.Printf("insert cheating submission error: %v", err)
		}

		_, err = db.Exec(`UPDATE contest_teams SET status = 'cheating_banned' WHERE contest_id = $1 AND team_id = $2`,
			contestID, teamID.Int64)
		if err == nil {
			if AnnounceCheating != nil {
				AnnounceCheating(db, contestIDInt, submitterTeamName, "提交了其他队伍的Flag")
			}
			// 广播作弊封禁事件
			monitor.AddMonitorEventToDB(db, contestID, "cheat", submitterTeamName, "", "")
		}

		_, err = db.Exec(`UPDATE contest_teams SET status = 'cheating_banned' WHERE contest_id = $1 AND team_id = $2`,
			contestID, cheatingVictimTeamID)
		if err == nil {
			if AnnounceCheating != nil {
				AnnounceCheating(db, contestIDInt, cheatingVictimTeamName, "Flag被其他队伍使用，涉嫌共享Flag")
			}
			// 广播作弊封禁事件
			monitor.AddMonitorEventToDB(db, contestID, "cheat", cheatingVictimTeamName, "", "")
		}

		// 广播大屏更新
		go func() {
			data := monitor.GetMonitorDataForBroadcast(db, contestID)
			monitor.BroadcastMonitorUpdate(contestID, data)
		}()

		c.JSON(http.StatusForbidden, gin.H{
			"error":   "CHEATING_DETECTED",
			"message": "检测到作弊行为：您提交了其他队伍的Flag！您的队伍已被封禁。",
		})
		return
	}

	score := 0
	bonusScore := 0
	solveOrder := 0
	firstBlood, secondBlood, thirdBlood := false, false, false
	if isCorrect {
		var solveCount int
		if contestMode == "awd-f" {
			db.QueryRow(`SELECT COUNT(*) FROM team_solves_awdf WHERE contest_id = $1 AND challenge_id = $2`, contestID, challengeID).Scan(&solveCount)
		} else {
			db.QueryRow(`SELECT COUNT(*) FROM team_solves WHERE contest_id = $1 AND challenge_id = $2`, contestID, challengeID).Scan(&solveCount)
		}

		// 计算解题顺序（用于动态分数计算）
		solveOrder = solveCount + 1

		// 计算当前题目动态分数（用于即时反馈，实际分数会动态更新）
		score = initialScore
		for i := 0; i < solveCount; i++ {
			score = int(float64(score) * 0.9)
		}
		if score < minScore {
			score = minScore
		}

		var firstBonus, secondBonus, thirdBonus int
		db.QueryRow(`SELECT COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).Scan(&firstBonus, &secondBonus, &thirdBonus)

		if solveCount == 0 {
			firstBlood = true
			bonusScore = score * firstBonus / 100
		} else if solveCount == 1 {
			secondBlood = true
			bonusScore = score * secondBonus / 100
		} else if solveCount == 2 {
			thirdBlood = true
			bonusScore = score * thirdBonus / 100
		}

		score += bonusScore
	}

	// 获取提交IP
	clientIP := c.ClientIP()

	_, err = db.Exec(`INSERT INTO submissions (contest_id, challenge_id, team_id, user_id, flag, is_correct, score, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		contestID, challengeID, teamID.Int64, userID, submittedFlag, isCorrect, score, clientIP)
	if err != nil {
		log.Printf("insert submission error: %v", err)
	}

	// 异步调用防作弊 WebSocket 推送
	contestIDInt64, _ := strconv.ParseInt(contestID, 10, 64)
	challengeIDInt64, _ := strconv.ParseInt(challengeID, 10, 64)
	go func() {
		// 检测同IP不同队伍提交正确答案
		if isCorrect {
			admin.CheckAndBroadcastSameIPDiffTeam(db, clientIP, challengeIDInt64, contestIDInt64, teamID.Int64)
		}
		// 检测不同队伍提交相同错误Flag
		if !isCorrect && len(submittedFlag) > 10 {
			admin.CheckAndBroadcastSameWrongFlag(db, submittedFlag, challengeIDInt64, contestIDInt64)
		}
		// 检测同一队伍多IP提交
		admin.CheckAndBroadcastMultiIP(db, teamID.Int64, contestIDInt64)
	}()

	if isCorrect {
		// AWD-F 和普通模式使用不同的解题记录表
		if contestMode == "awd-f" {
			_, err = db.Exec(`INSERT INTO team_solves_awdf (contest_id, challenge_id, team_id, first_solver_id, solve_order)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (contest_id, challenge_id, team_id) DO NOTHING`,
				contestID, challengeID, teamID.Int64, userID, solveOrder)
		} else {
			_, err = db.Exec(`INSERT INTO team_solves (contest_id, challenge_id, team_id, first_solver_id, solve_order)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (contest_id, challenge_id, team_id) DO NOTHING`,
				contestID, challengeID, teamID.Int64, userID, solveOrder)
		}
		if err != nil {
			log.Printf("insert team_solve error: %v", err)
		}

		if (firstBlood || secondBlood || thirdBlood) && AnnounceBlood != nil {
			var challengeName string
			if contestMode == "awd-f" {
				db.QueryRow(`SELECT q.title FROM question_bank_awdf q JOIN contest_challenges_awdf cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
			} else {
				db.QueryRow(`SELECT q.title FROM question_bank q JOIN contest_challenges cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
			}
			var teamName string
			db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
			contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
			if firstBlood {
				AnnounceBlood(db, contestIDInt, challengeName, teamName, 1)
			} else if secondBlood {
				AnnounceBlood(db, contestIDInt, challengeName, teamName, 2)
			} else if thirdBlood {
				AnnounceBlood(db, contestIDInt, challengeName, teamName, 3)
			}
		}
	}

	resp := SubmitFlagResponse{
		Correct:     isCorrect,
		Score:       score,
		FirstBlood:  firstBlood,
		SecondBlood: secondBlood,
		ThirdBlood:  thirdBlood,
	}

	if isCorrect {
		if firstBlood {
			resp.Message = "一血！恭喜！"
		} else if secondBlood {
			resp.Message = "二血！恭喜！"
		} else if thirdBlood {
			resp.Message = "三血！恭喜！"
		} else {
			resp.Message = "回答正确！"
		}
	} else {
		resp.Message = "Flag错误"
	}

	// 记录Flag提交日志（包含提交次数）
	contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
	challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
	var teamName, challengeName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
	if contestMode == "awd-f" {
		db.QueryRow(`SELECT q.title FROM question_bank_awdf q JOIN contest_challenges_awdf cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
	} else {
		db.QueryRow(`SELECT q.title FROM question_bank q JOIN contest_challenges cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
	}
	
	// 获取该队伍在该题目的提交次数
	var submitCount int
	db.QueryRow(`SELECT COUNT(*) FROM submissions WHERE contest_id = $1 AND challenge_id = $2 AND team_id = $3`,
		contestID, challengeID, teamID.Int64).Scan(&submitCount)
	
	if isCorrect {
		logs.WriteLog(db, logs.TypeFlagSubmit, logs.LevelSuccess, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
			"队伍 ["+teamName+"] 提交题目 ["+challengeName+"] 的答案 — 正确 | Flag: "+submittedFlag, map[string]interface{}{
				"flag": submittedFlag, "score": score, "submitCount": submitCount,
			})
		// 广播大屏更新
		go func() {
			data := monitor.GetMonitorDataForBroadcast(db, contestID)
			monitor.BroadcastMonitorUpdate(contestID, data)
		}()
	} else {
		logs.WriteLog(db, logs.TypeFlagSubmit, logs.LevelError, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
			"队伍 ["+teamName+"] 提交题目 ["+challengeName+"] 的答案 — 错误 | Flag: "+submittedFlag, map[string]interface{}{
				"flag": submittedFlag, "submitCount": submitCount,
			})
		// 广播尝试解题事件
		var userName string
		db.QueryRow(`SELECT COALESCE(display_name, username) FROM users WHERE id = $1`, userID).Scan(&userName)
		go func() {
			monitor.AddMonitorEventToDB(db, contestID, "attempt", teamName, userName, challengeName)
			data := monitor.GetMonitorDataForBroadcast(db, contestID)
			monitor.BroadcastMonitorUpdate(contestID, data)
		}()
	}

	c.JSON(http.StatusOK, resp)
}

// HandleGetTeamSolves 获取队伍已解题目列表（动态分数计算）
func HandleGetTeamSolves(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	userID := c.GetInt64("userID")

	var teamID sql.NullInt64
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(http.StatusOK, gin.H{"solves": []int64{}, "totalScore": 0})
		return
	}

	// 获取比赛模式和血量奖励配置
	var contestMode string
	var firstBonus, secondBonus, thirdBonus int
	db.QueryRow(`SELECT mode, COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).Scan(&contestMode, &firstBonus, &secondBonus, &thirdBonus)

	// 获取队伍解题记录
	rows, err := db.Query(`SELECT challenge_id, solve_order, solved_at FROM team_solves WHERE contest_id = $1 AND team_id = $2`,
		contestID, teamID.Int64)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	type SolveInfo struct {
		ChallengeID int64  `json:"challengeId"`
		Score       int    `json:"score"`
		SolvedAt    string `json:"solvedAt"`
		SolveOrder  int    `json:"solveOrder"`
	}

	var solves []SolveInfo
	var challengeIDs []int64
	solveOrderMap := make(map[int64]int)
	solvedAtMap := make(map[int64]string)
	for rows.Next() {
		var challengeID int64
		var solveOrder int
		var solvedAt time.Time
		if err := rows.Scan(&challengeID, &solveOrder, &solvedAt); err != nil {
			continue
		}
		challengeIDs = append(challengeIDs, challengeID)
		solveOrderMap[challengeID] = solveOrder
		solvedAtMap[challengeID] = solvedAt.Format("2006-01-02 15:04:05")
	}

	totalScore := 0
	// 对每道解出的题目实时计算动态分数
	for _, challengeID := range challengeIDs {
		// 获取题目配置
		var initialScore, minScore, difficulty int
		if contestMode == "awd-f" {
			err = db.QueryRow(`SELECT initial_score, min_score, 5 FROM contest_challenges_awdf WHERE id = $1`, challengeID).Scan(&initialScore, &minScore, &difficulty)
		} else {
			err = db.QueryRow(`SELECT initial_score, min_score, difficulty FROM contest_challenges WHERE id = $1`, challengeID).Scan(&initialScore, &minScore, &difficulty)
		}
		if err != nil {
			continue
		}

		// 获取该题当前解题人数
		var solveCount int
		if contestMode == "awd-f" {
			db.QueryRow(`SELECT COUNT(*) FROM team_solves_awdf WHERE contest_id = $1 AND challenge_id = $2`, contestID, challengeID).Scan(&solveCount)
		} else {
			db.QueryRow(`SELECT COUNT(*) FROM team_solves WHERE contest_id = $1 AND challenge_id = $2`, contestID, challengeID).Scan(&solveCount)
		}

		// 计算当前动态分数
		baseScore := CalculateDynamicScore(initialScore, minScore, difficulty, solveCount)

		// 根据解题顺序计算血量奖励
		solveOrder := solveOrderMap[challengeID]
		score := CalculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)

		solves = append(solves, SolveInfo{
			ChallengeID: challengeID,
			Score:       score,
			SolvedAt:    solvedAtMap[challengeID],
			SolveOrder:  solveOrder,
		})
		totalScore += score
	}

	if solves == nil {
		solves = []SolveInfo{}
	}

	// AWD-F 模式：获取防守得分
	defenseScore := 0
	if contestMode == "awd-f" {
		db.QueryRow(`SELECT COALESCE(SUM(score_earned), 0) FROM awdf_exp_results WHERE contest_id = $1 AND team_id = $2`, contestID, teamID.Int64).Scan(&defenseScore)
	}

	c.JSON(http.StatusOK, gin.H{
		"solves":       solves,
		"totalScore":   totalScore + defenseScore, // 总分 = 攻击得分 + 防守得分
		"attackScore":  totalScore,                 // 攻击得分（解题）
		"defenseScore": defenseScore,               // 防守得分
		"teamId":       teamID.Int64,
	})
}

// CalculateDynamicScore 计算题目当前动态分数 - 使用指数衰减公式
// S(N) = Smin + (Smax - Smin) × e^(-(N-1)/(10D))
func CalculateDynamicScore(initialScore, minScore, difficulty, solveCount int) int {
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

// CalculateScoreWithBonus 计算包含血量奖励的分数
func CalculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus int) int {
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

// HandleGetChallengeStats 获取题目解题统计
func HandleGetChallengeStats(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Param("challengeId")
	userID := c.GetInt64("userID")
	clientIP := c.ClientIP()

	// 获取比赛模式
	var contestMode string
	db.QueryRow(`SELECT mode FROM contests WHERE id = $1`, contestID).Scan(&contestMode)

	// 记录首次查看时间（用于计算解题用时）
	var teamID sql.NullInt64
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if teamID.Valid {
		// 记录队伍首次查看题目的时间（已存在则忽略）
		result, err := db.Exec(`INSERT INTO challenge_first_views (contest_id, challenge_id, team_id, user_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (contest_id, challenge_id, team_id) DO NOTHING`,
			contestID, challengeID, teamID.Int64, userID)
		
		// 如果是首次查看，记录系统日志
		if err == nil && result != nil {
			if rowsAffected, _ := result.RowsAffected(); rowsAffected > 0 {
				contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
				challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
				var teamName, challengeName, userName string
				db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
				if contestMode == "awd-f" {
					db.QueryRow(`SELECT q.title FROM question_bank_awdf q JOIN contest_challenges_awdf cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
				} else {
					db.QueryRow(`SELECT q.title FROM question_bank q JOIN contest_challenges cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
				}
				db.QueryRow(`SELECT COALESCE(display_name, username) FROM users WHERE id = $1`, userID).Scan(&userName)
				
				logs.WriteLog(db, logs.TypeChallengeView, logs.LevelInfo, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
					"队伍 ["+teamName+"] 成员 ["+userName+"] 首次查看题目 ["+challengeName+"]", nil)
			}
		}
	}

	var solveCount int
	var rows *sql.Rows
	var err error
	if contestMode == "awd-f" {
		db.QueryRow(`SELECT COUNT(*) FROM team_solves_awdf WHERE contest_id = $1 AND challenge_id = $2`, contestID, challengeID).Scan(&solveCount)
		// 查询所有解题者（按时间排序）
		rows, err = db.Query(`
			SELECT ts.team_id, t.name, ts.solved_at
			FROM team_solves_awdf ts
			JOIN teams t ON ts.team_id = t.id
			WHERE ts.contest_id = $1 AND ts.challenge_id = $2
			ORDER BY ts.solved_at ASC`, contestID, challengeID)
	} else {
		db.QueryRow(`SELECT COUNT(*) FROM team_solves WHERE contest_id = $1 AND challenge_id = $2`, contestID, challengeID).Scan(&solveCount)
		// 查询所有解题者（按时间排序）
		rows, err = db.Query(`
			SELECT ts.team_id, t.name, ts.solved_at
			FROM team_solves ts
			JOIN teams t ON ts.team_id = t.id
			WHERE ts.contest_id = $1 AND ts.challenge_id = $2
			ORDER BY ts.solved_at ASC`, contestID, challengeID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	type SolverInfo struct {
		Rank     int    `json:"rank"`
		TeamID   int64  `json:"teamId"`
		TeamName string `json:"teamName"`
		SolvedAt string `json:"solvedAt"`
	}

	var solvers []SolverInfo
	rank := 1
	for rows.Next() {
		var s SolverInfo
		var solvedAt time.Time
		if err := rows.Scan(&s.TeamID, &s.TeamName, &solvedAt); err != nil {
			continue
		}
		s.Rank = rank
		s.SolvedAt = solvedAt.Format("2006-01-02 15:04:05")
		solvers = append(solvers, s)
		rank++
	}

	resp := gin.H{"solveCount": solveCount, "solvers": solvers}

	if len(solvers) > 0 {
		resp["firstBlood"] = solvers[0]
	}
	if len(solvers) > 1 {
		resp["secondBlood"] = solvers[1]
	}
	if len(solvers) > 2 {
		resp["thirdBlood"] = solvers[2]
	}

	// AWD-F 模式：返回防守者列表
	if contestMode == "awd-f" {
		type DefenderInfo struct {
			Rank          int    `json:"rank"`
			TeamID        int64  `json:"teamId"`
			TeamName      string `json:"teamName"`
			DefenseCount  int    `json:"defenseCount"`
			TotalScore    int    `json:"totalScore"`
			LastDefenseAt string `json:"lastDefenseAt,omitempty"`
		}

		// 查询防守成功的队伍（从 awdf_exp_results 表查询 defense_success = true 的记录）
		defenderRows, err := db.Query(`
			SELECT t.id, t.name, 
				COUNT(*) as defense_count, 
				SUM(COALESCE(score_earned, 0)) as total_score,
				MAX(executed_at) as last_defense_at
			FROM awdf_exp_results er
			JOIN teams t ON er.team_id = t.id
			WHERE er.contest_id = $1 AND er.challenge_id = $2 AND er.defense_success = true
			GROUP BY t.id, t.name
			ORDER BY total_score DESC, last_defense_at ASC`, contestID, challengeID)

		var defenders []DefenderInfo
		if err == nil {
			defer defenderRows.Close()
			defRank := 1
			for defenderRows.Next() {
				var d DefenderInfo
				var lastDefenseAt sql.NullTime
				if err := defenderRows.Scan(&d.TeamID, &d.TeamName, &d.DefenseCount, &d.TotalScore, &lastDefenseAt); err != nil {
					continue
				}
				d.Rank = defRank
				if lastDefenseAt.Valid {
					d.LastDefenseAt = lastDefenseAt.Time.Format("2006-01-02 15:04:05")
				}
				defenders = append(defenders, d)
				defRank++
			}
		}

		resp["defenders"] = defenders
		resp["defenseCount"] = len(defenders)
		resp["mode"] = "awd-f"
	}

	c.JSON(http.StatusOK, resp)
}

// HandleGetAllChallengesBlood 批量获取所有题目的血量统计（不记录首次查看）
func HandleGetAllChallengesBlood(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 获取比赛模式
	var contestMode string
	db.QueryRow(`SELECT mode FROM contests WHERE id = $1`, contestID).Scan(&contestMode)

	// 根据比赛模式选择不同的表
	var querySQL string
	if contestMode == "awd-f" {
		querySQL = `
			WITH solve_stats AS (
				SELECT 
					ts.challenge_id,
					COUNT(*) as solve_count,
					MIN(ts.solved_at) as first_solve_time
				FROM team_solves_awdf ts
				WHERE ts.contest_id = $1
				GROUP BY ts.challenge_id
			),
			bloods AS (
				SELECT 
					ts.challenge_id,
					ts.team_id,
					t.name as team_name,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves_awdf ts
				JOIN teams t ON ts.team_id = t.id
				WHERE ts.contest_id = $1
			)
			SELECT 
				cc.id as challenge_id,
				COALESCE(ss.solve_count, 0) as solve_count,
				b1.team_name as first_blood,
				b2.team_name as second_blood,
				b3.team_name as third_blood
			FROM contest_challenges_awdf cc
			LEFT JOIN solve_stats ss ON cc.id = ss.challenge_id
			LEFT JOIN bloods b1 ON cc.id = b1.challenge_id AND b1.blood_rank = 1
			LEFT JOIN bloods b2 ON cc.id = b2.challenge_id AND b2.blood_rank = 2
			LEFT JOIN bloods b3 ON cc.id = b3.challenge_id AND b3.blood_rank = 3
			WHERE cc.contest_id = $1`
	} else {
		querySQL = `
			WITH solve_stats AS (
				SELECT 
					ts.challenge_id,
					COUNT(*) as solve_count,
					MIN(ts.solved_at) as first_solve_time
				FROM team_solves ts
				WHERE ts.contest_id = $1
				GROUP BY ts.challenge_id
			),
			bloods AS (
				SELECT 
					ts.challenge_id,
					ts.team_id,
					t.name as team_name,
					ROW_NUMBER() OVER (PARTITION BY ts.challenge_id ORDER BY ts.solved_at ASC) as blood_rank
				FROM team_solves ts
				JOIN teams t ON ts.team_id = t.id
				WHERE ts.contest_id = $1
			)
			SELECT 
				cc.id as challenge_id,
				COALESCE(ss.solve_count, 0) as solve_count,
				b1.team_name as first_blood,
				b2.team_name as second_blood,
				b3.team_name as third_blood
			FROM contest_challenges cc
			LEFT JOIN solve_stats ss ON cc.id = ss.challenge_id
			LEFT JOIN bloods b1 ON cc.id = b1.challenge_id AND b1.blood_rank = 1
			LEFT JOIN bloods b2 ON cc.id = b2.challenge_id AND b2.blood_rank = 2
			LEFT JOIN bloods b3 ON cc.id = b3.challenge_id AND b3.blood_rank = 3
			WHERE cc.contest_id = $1`
	}

	// 获取所有题目的解题统计
	rows, err := db.Query(querySQL, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	type ChallengeBlood struct {
		ChallengeID int64          `json:"challengeId"`
		SolveCount  int            `json:"solveCount"`
		FirstBlood  sql.NullString `json:"-"`
		SecondBlood sql.NullString `json:"-"`
		ThirdBlood  sql.NullString `json:"-"`
		First       string         `json:"firstBlood,omitempty"`
		Second      string         `json:"secondBlood,omitempty"`
		Third       string         `json:"thirdBlood,omitempty"`
	}

	var results []ChallengeBlood
	for rows.Next() {
		var cb ChallengeBlood
		if err := rows.Scan(&cb.ChallengeID, &cb.SolveCount, &cb.FirstBlood, &cb.SecondBlood, &cb.ThirdBlood); err != nil {
			continue
		}
		if cb.FirstBlood.Valid {
			cb.First = cb.FirstBlood.String
		}
		if cb.SecondBlood.Valid {
			cb.Second = cb.SecondBlood.String
		}
		if cb.ThirdBlood.Valid {
			cb.Third = cb.ThirdBlood.String
		}
		results = append(results, cb)
	}

	if results == nil {
		results = []ChallengeBlood{}
	}

	c.JSON(http.StatusOK, results)
}

// HandleGetScoreboard 获取排行榜（动态分数计算）
func HandleGetScoreboard(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 获取比赛模式和血量奖励配置
	var contestMode string
	var firstBonus, secondBonus, thirdBonus int
	db.QueryRow(`SELECT mode, COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).Scan(&contestMode, &firstBonus, &secondBonus, &thirdBonus)

	// 获取每道题的当前解题人数（用于计算动态分数）
	challengeSolveCountMap := make(map[int64]int)
	countRows, _ := db.Query(`SELECT challenge_id, COUNT(*) FROM team_solves WHERE contest_id = $1 GROUP BY challenge_id`, contestID)
	if countRows != nil {
		for countRows.Next() {
			var cid int64
			var cnt int
			countRows.Scan(&cid, &cnt)
			challengeSolveCountMap[cid] = cnt
		}
		countRows.Close()
	}

	// 获取每道题的配置（初始分数、最低分数）
	challengeConfigMap := make(map[int64]struct{ InitialScore, MinScore, Difficulty int })
	var configRows *sql.Rows
	if contestMode == "awd-f" {
		configRows, _ = db.Query(`SELECT id, initial_score, min_score, 5 FROM contest_challenges_awdf WHERE contest_id = $1`, contestID)
	} else {
		configRows, _ = db.Query(`SELECT id, initial_score, min_score, difficulty FROM contest_challenges WHERE contest_id = $1`, contestID)
	}
	if configRows != nil {
		for configRows.Next() {
			var cid int64
			var initial, min, diff int
			configRows.Scan(&cid, &initial, &min, &diff)
			challengeConfigMap[cid] = struct{ InitialScore, MinScore, Difficulty int }{initial, min, diff}
		}
		configRows.Close()
	}

	// AWD-F 模式：获取每个队伍的防守得分
	teamDefenseScoreMap := make(map[int64]int)
	if contestMode == "awd-f" {
		defenseRows, _ := db.Query(`SELECT team_id, COALESCE(SUM(score_earned), 0) FROM awdf_exp_results WHERE contest_id = $1 GROUP BY team_id`, contestID)
		if defenseRows != nil {
			for defenseRows.Next() {
				var teamID int64
				var defenseScore int
				defenseRows.Scan(&teamID, &defenseScore)
				teamDefenseScoreMap[teamID] = defenseScore
			}
			defenseRows.Close()
		}
	}

	// 获取所有队伍的解题记录
	rows, err := db.Query(`
		SELECT ts.team_id, t.name, t.avatar, t.captain_id, u.avatar as captain_avatar,
		       ts.challenge_id, ts.solve_order, ts.solved_at
		FROM team_solves ts
		JOIN teams t ON ts.team_id = t.id
		LEFT JOIN users u ON t.captain_id = u.id
		WHERE ts.contest_id = $1
		ORDER BY ts.team_id, ts.solved_at`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	type TeamScore struct {
		Rank         int     `json:"rank"`
		TeamID       int64   `json:"teamId"`
		TeamName     string  `json:"teamName"`
		Avatar       *string `json:"avatar"`
		TotalScore   int     `json:"totalScore"`
		AttackScore  int     `json:"attackScore,omitempty"`  // AWD-F: 攻击得分（解题）
		DefenseScore int     `json:"defenseScore,omitempty"` // AWD-F: 防守得分
		SolveCount   int     `json:"solveCount"`
		LastSolve    string  `json:"lastSolve"`
	}

	teamScoreMap := make(map[int64]*TeamScore)
	teamLastSolveMap := make(map[int64]time.Time)

	for rows.Next() {
		var teamID int64
		var teamName string
		var teamAvatar, captainAvatar sql.NullString
		var captainID sql.NullInt64
		var challengeID int64
		var solveOrder int
		var solvedAt time.Time

		if err := rows.Scan(&teamID, &teamName, &teamAvatar, &captainID, &captainAvatar, &challengeID, &solveOrder, &solvedAt); err != nil {
			continue
		}

		// 初始化队伍数据
		if _, exists := teamScoreMap[teamID]; !exists {
			ts := &TeamScore{
				TeamID:   teamID,
				TeamName: teamName,
			}
			// 队伍头像优先，如为空则使用队长头像
			if teamAvatar.Valid && teamAvatar.String != "" {
				ts.Avatar = &teamAvatar.String
			} else if captainAvatar.Valid && captainAvatar.String != "" {
				ts.Avatar = &captainAvatar.String
			}
			teamScoreMap[teamID] = ts
		}

		// 计算该题的动态分数
		config := challengeConfigMap[challengeID]
		solveCount := challengeSolveCountMap[challengeID]
		baseScore := CalculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
		score := CalculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)

		teamScoreMap[teamID].AttackScore += score
		teamScoreMap[teamID].SolveCount++

		// 记录最后解题时间
		if solvedAt.After(teamLastSolveMap[teamID]) {
			teamLastSolveMap[teamID] = solvedAt
		}
	}

	// AWD-F 模式：将防守得分加入
	if contestMode == "awd-f" {
		for teamID, defenseScore := range teamDefenseScoreMap {
			if ts, exists := teamScoreMap[teamID]; exists {
				ts.DefenseScore = defenseScore
			} else {
				// 队伍没有解题但有防守得分的情况
				var teamName string
				var teamAvatar, captainAvatar sql.NullString
				db.QueryRow(`SELECT t.name, t.avatar, u.avatar FROM teams t LEFT JOIN users u ON t.captain_id = u.id WHERE t.id = $1`, teamID).Scan(&teamName, &teamAvatar, &captainAvatar)
				ts := &TeamScore{
					TeamID:       teamID,
					TeamName:     teamName,
					DefenseScore: defenseScore,
				}
				if teamAvatar.Valid && teamAvatar.String != "" {
					ts.Avatar = &teamAvatar.String
				} else if captainAvatar.Valid && captainAvatar.String != "" {
					ts.Avatar = &captainAvatar.String
				}
				teamScoreMap[teamID] = ts
			}
		}
	}

	// 转换为数组并排序
	var scores []TeamScore
	for teamID, ts := range teamScoreMap {
		// 计算总分 = 攻击得分 + 防守得分
		ts.TotalScore = ts.AttackScore + ts.DefenseScore
		if lastSolve, ok := teamLastSolveMap[teamID]; ok {
			ts.LastSolve = lastSolve.Format("2006-01-02 15:04:05")
		} else {
			ts.LastSolve = "1970-01-01 00:00:00" // 没有解题的队伍
		}
		scores = append(scores, *ts)
	}

	// 按总分降序、最后解题时间升序排序
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].TotalScore != scores[j].TotalScore {
			return scores[i].TotalScore > scores[j].TotalScore
		}
		return scores[i].LastSolve < scores[j].LastSolve
	})

	// 设置排名
	for i := range scores {
		scores[i].Rank = i + 1
	}

	if scores == nil {
		scores = []TeamScore{}
	}

	c.JSON(http.StatusOK, scores)
}

// HandleGetSoloScoreboard 获取个人排行榜（动态分数计算）
func HandleGetSoloScoreboard(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 获取比赛模式和血量奖励配置
	var contestMode string
	var firstBonus, secondBonus, thirdBonus int
	db.QueryRow(`SELECT mode, COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).Scan(&contestMode, &firstBonus, &secondBonus, &thirdBonus)

	// 获取每道题的当前解题人数（用于计算动态分数）
	challengeSolveCountMap := make(map[int64]int)
	countRows, _ := db.Query(`SELECT challenge_id, COUNT(*) FROM team_solves WHERE contest_id = $1 GROUP BY challenge_id`, contestID)
	if countRows != nil {
		for countRows.Next() {
			var cid int64
			var cnt int
			countRows.Scan(&cid, &cnt)
			challengeSolveCountMap[cid] = cnt
		}
		countRows.Close()
	}

	// 获取每道题的配置（初始分数、最低分数）
	challengeConfigMap := make(map[int64]struct{ InitialScore, MinScore, Difficulty int })
	var configRows *sql.Rows
	if contestMode == "awd-f" {
		configRows, _ = db.Query(`SELECT id, initial_score, min_score, 5 FROM contest_challenges_awdf WHERE contest_id = $1`, contestID)
	} else {
		configRows, _ = db.Query(`SELECT id, initial_score, min_score, difficulty FROM contest_challenges WHERE contest_id = $1`, contestID)
	}
	if configRows != nil {
		for configRows.Next() {
			var cid int64
			var initial, min, diff int
			configRows.Scan(&cid, &initial, &min, &diff)
			challengeConfigMap[cid] = struct{ InitialScore, MinScore, Difficulty int }{initial, min, diff}
		}
		configRows.Close()
	}

	// 获取每道题的解题顺序（从 team_solves 表）
	challengeSolveOrderMap := make(map[int64]map[int64]int) // challengeID -> teamID -> solveOrder
	orderRows, _ := db.Query(`SELECT challenge_id, team_id, solve_order FROM team_solves WHERE contest_id = $1`, contestID)
	if orderRows != nil {
		for orderRows.Next() {
			var cid, tid int64
			var order int
			orderRows.Scan(&cid, &tid, &order)
			if challengeSolveOrderMap[cid] == nil {
				challengeSolveOrderMap[cid] = make(map[int64]int)
			}
			challengeSolveOrderMap[cid][tid] = order
		}
		orderRows.Close()
	}

	// 获取用户解题记录（包含队伍信息）
	rows, err := db.Query(`
		SELECT s.user_id, s.challenge_id, s.submitted_at,
		       COALESCE(u.display_name, u.username) as display_name,
		       u.avatar as user_avatar, u.team_id, t.name as team_name
		FROM submissions s
		JOIN users u ON s.user_id = u.id
		LEFT JOIN teams t ON u.team_id = t.id
		WHERE s.contest_id = $1 AND s.is_correct = true
		ORDER BY s.user_id, s.submitted_at`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	type UserScore struct {
		Rank       int     `json:"rank"`
		UserID     int64   `json:"userId"`
		UserName   string  `json:"userName"`
		Avatar     *string `json:"avatar"`
		TeamID     int64   `json:"teamId,omitempty"`
		TeamName   string  `json:"teamName,omitempty"`
		SolveCount int     `json:"solveCount"`
		TotalScore int     `json:"totalScore"`
		LastSolve  string  `json:"lastSolve"`
	}

	userScoreMap := make(map[int64]*UserScore)
	userLastSolveMap := make(map[int64]time.Time)
	userSolvedChallenges := make(map[int64]map[int64]bool) // userID -> challengeID -> solved

	for rows.Next() {
		var userID, challengeID int64
		var submittedAt time.Time
		var displayName string
		var userAvatar sql.NullString
		var teamID sql.NullInt64
		var teamName sql.NullString

		if err := rows.Scan(&userID, &challengeID, &submittedAt, &displayName, &userAvatar, &teamID, &teamName); err != nil {
			continue
		}

		// 初始化用户数据
		if _, exists := userScoreMap[userID]; !exists {
			us := &UserScore{
				UserID:   userID,
				UserName: displayName,
			}
			if userAvatar.Valid && userAvatar.String != "" {
				us.Avatar = &userAvatar.String
			}
			if teamID.Valid {
				us.TeamID = teamID.Int64
			}
			if teamName.Valid {
				us.TeamName = teamName.String
			}
			userScoreMap[userID] = us
			userSolvedChallenges[userID] = make(map[int64]bool)
		}

		// 跳过已经计算过的题目
		if userSolvedChallenges[userID][challengeID] {
			continue
		}
		userSolvedChallenges[userID][challengeID] = true

		// 计算该题的动态分数
		config := challengeConfigMap[challengeID]
		solveCount := challengeSolveCountMap[challengeID]
		baseScore := CalculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)

		// 获取用户所属队伍的解题顺序
		solveOrder := 0
		if teamID.Valid && challengeSolveOrderMap[challengeID] != nil {
			solveOrder = challengeSolveOrderMap[challengeID][teamID.Int64]
		}
		score := CalculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)

		userScoreMap[userID].TotalScore += score
		userScoreMap[userID].SolveCount++

		// 记录最后解题时间
		if submittedAt.After(userLastSolveMap[userID]) {
			userLastSolveMap[userID] = submittedAt
		}
	}

	// 转换为数组并排序
	var scores []UserScore

	// AWD-F 模式：获取每个队伍的防守得分
	teamDefenseScoreMap := make(map[int64]int)
	teamMemberCountMap := make(map[int64]int) // 队伍中有解题记录的成员数
	if contestMode == "awd-f" {
		defenseRows, _ := db.Query(`SELECT team_id, COALESCE(SUM(score_earned), 0) FROM awdf_exp_results WHERE contest_id = $1 GROUP BY team_id`, contestID)
		if defenseRows != nil {
			for defenseRows.Next() {
				var teamID int64
				var defenseScore int
				defenseRows.Scan(&teamID, &defenseScore)
				teamDefenseScoreMap[teamID] = defenseScore
			}
			defenseRows.Close()
		}
		// 统计每个队伍有解题记录的成员数
		for _, us := range userScoreMap {
			if us.TeamID != 0 {
				teamMemberCountMap[us.TeamID]++
			}
		}
	}

	for userID, us := range userScoreMap {
		us.LastSolve = userLastSolveMap[userID].Format("2006-01-02 15:04:05")
		// AWD-F 模式：将队伍防守得分平均分配给每个队员
		if contestMode == "awd-f" && us.TeamID != 0 {
			memberCount := teamMemberCountMap[us.TeamID]
			if memberCount > 0 {
				us.TotalScore += teamDefenseScoreMap[us.TeamID] / memberCount
			}
		}
		scores = append(scores, *us)
	}

	// 按总分降序、最后解题时间升序排序
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].TotalScore != scores[j].TotalScore {
			return scores[i].TotalScore > scores[j].TotalScore
		}
		return scores[i].LastSolve < scores[j].LastSolve
	})

	// 设置排名
	for i := range scores {
		scores[i].Rank = i + 1
	}

	if scores == nil {
		scores = []UserScore{}
	}

	c.JSON(http.StatusOK, scores)
}

// HandleGetScoreTrend 获取分数趋势（动态分数计算）
func HandleGetScoreTrend(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 获取比赛模式和血量奖励配置
	var contestMode string
	var firstBonus, secondBonus, thirdBonus int
	db.QueryRow(`SELECT mode, COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1) FROM contests WHERE id = $1`, contestID).Scan(&contestMode, &firstBonus, &secondBonus, &thirdBonus)

	// 获取每道题的当前解题人数（用于计算动态分数）
	challengeSolveCountMap := make(map[int64]int)
	countRows, _ := db.Query(`SELECT challenge_id, COUNT(*) FROM team_solves WHERE contest_id = $1 GROUP BY challenge_id`, contestID)
	if countRows != nil {
		for countRows.Next() {
			var cid int64
			var cnt int
			countRows.Scan(&cid, &cnt)
			challengeSolveCountMap[cid] = cnt
		}
		countRows.Close()
	}

	// 获取每道题的配置（初始分数、最低分数）
	challengeConfigMap := make(map[int64]struct{ InitialScore, MinScore, Difficulty int })
	var configRows *sql.Rows
	if contestMode == "awd-f" {
		configRows, _ = db.Query(`SELECT id, initial_score, min_score, 5 FROM contest_challenges_awdf WHERE contest_id = $1`, contestID)
	} else {
		configRows, _ = db.Query(`SELECT id, initial_score, min_score, difficulty FROM contest_challenges WHERE contest_id = $1`, contestID)
	}
	if configRows != nil {
		for configRows.Next() {
			var cid int64
			var initial, min, diff int
			configRows.Scan(&cid, &initial, &min, &diff)
			challengeConfigMap[cid] = struct{ InitialScore, MinScore, Difficulty int }{initial, min, diff}
		}
		configRows.Close()
	}

	// 获取前5名队伍（基于当前动态分数）
	type TeamTotalScore struct {
		TeamID int64
		Name   string
		Total  int
	}
	var teamTotals []TeamTotalScore

	// 获取所有队伍解题记录用于计算当前总分
	allTeamsRows, _ := db.Query(`
		SELECT ts.team_id, t.name, ts.challenge_id, ts.solve_order
		FROM team_solves ts
		JOIN teams t ON ts.team_id = t.id
		WHERE ts.contest_id = $1`, contestID)
	
	teamScores := make(map[int64]int)
	teamNames := make(map[int64]string)
	if allTeamsRows != nil {
		for allTeamsRows.Next() {
			var teamID, challengeID int64
			var teamName string
			var solveOrder int
			allTeamsRows.Scan(&teamID, &teamName, &challengeID, &solveOrder)
			teamNames[teamID] = teamName
			
			config := challengeConfigMap[challengeID]
			solveCount := challengeSolveCountMap[challengeID]
			baseScore := CalculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
			score := CalculateScoreWithBonus(baseScore, solveOrder, firstBonus, secondBonus, thirdBonus)
			teamScores[teamID] += score
		}
		allTeamsRows.Close()
	}

	for teamID, total := range teamScores {
		teamTotals = append(teamTotals, TeamTotalScore{TeamID: teamID, Name: teamNames[teamID], Total: total})
	}
	sort.Slice(teamTotals, func(i, j int) bool {
		return teamTotals[i].Total > teamTotals[j].Total
	})

	if len(teamTotals) > 5 {
		teamTotals = teamTotals[:5]
	}

	if len(teamTotals) == 0 {
		c.JSON(http.StatusOK, gin.H{"labels": []string{}, "teams": []interface{}{}})
		return
	}

	// 获取前5名队伍的解题时间线
	top5TeamIDs := make([]int64, len(teamTotals))
	for i, t := range teamTotals {
		top5TeamIDs[i] = t.TeamID
	}

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

	// 获取这5个队伍的解题记录
	for _, teamID := range top5TeamIDs {
		rows, err := db.Query(`
			SELECT challenge_id, solve_order, solved_at
			FROM team_solves
			WHERE contest_id = $1 AND team_id = $2
			ORDER BY solved_at ASC`, contestID, teamID)
		if err != nil {
			continue
		}
		
		for rows.Next() {
			var challengeID int64
			var solveOrder int
			var solvedAt time.Time
			if err := rows.Scan(&challengeID, &solveOrder, &solvedAt); err != nil {
				continue
			}

			if _, exists := teamData[teamID]; !exists {
				teamData[teamID] = struct {
					Name   string
					Solves []SolveRecord
				}{Name: teamNames[teamID], Solves: []SolveRecord{}}
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
		rows.Close()
	}

	if len(teamData) == 0 {
		c.JSON(http.StatusOK, gin.H{"labels": []string{}, "teams": []interface{}{}})
		return
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

	// 按当前动态分数计算每个时间点的累计分数
	for _, teamID := range teamOrder {
		data := teamData[teamID]
		var scores []int
		for _, ts := range timestamps {
			cumScore := 0
			for _, solve := range data.Solves {
				if !solve.SolvedAt.After(ts) {
					config := challengeConfigMap[solve.ChallengeID]
					solveCount := challengeSolveCountMap[solve.ChallengeID]
					baseScore := CalculateDynamicScore(config.InitialScore, config.MinScore, config.Difficulty, solveCount)
					score := CalculateScoreWithBonus(baseScore, solve.SolveOrder, firstBonus, secondBonus, thirdBonus)
					cumScore += score
				}
			}
			scores = append(scores, cumScore)
		}
		teamTrends = append(teamTrends, TeamTrend{Name: data.Name, Scores: scores})
	}

	c.JSON(http.StatusOK, gin.H{
		"labels": labels,
		"teams":  teamTrends,
	})
}
