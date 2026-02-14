// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package question

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"tgctf/server/monitor"
)

// AnnounceChallengeFunc 题目状态变更公告函数类型
type AnnounceChallengeFunc func(db *sql.DB, contestID int64, challengeName, action string)

// GenerateFlagsFunc Flag生成函数类型（为指定比赛的指定题目生成所有已审核队伍的Flag）
type GenerateFlagsFunc func(db *sql.DB, contestID, challengeID string)

// AnnounceChallenge 全局变量，用于注入题目状态变更公告函数
var AnnounceChallenge AnnounceChallengeFunc

// GenerateTeamChallengeFlag 全局变量，用于注入Flag生成函数
var GenerateTeamChallengeFlag GenerateFlagsFunc

// ContestChallenge 比赛题目关联
type ContestChallenge struct {
	ID                int64          `json:"id"`
	ContestID         int64          `json:"contestId"`
	QuestionID        int64          `json:"questionId"`
	Title             string         `json:"title"`
	Type              string         `json:"type"`
	CategoryID        int64          `json:"categoryId"`
	CategoryName      sql.NullString `json:"categoryName"`
	Difficulty        int            `json:"difficulty"`
	Description       sql.NullString `json:"description"`
	Flag              sql.NullString `json:"flag,omitempty"`
	FlagType          string         `json:"flagType"`
	DockerImage       sql.NullString `json:"dockerImage"`
	Attachment        sql.NullString `json:"attachmentUrl"`
	Ports             sql.NullString `json:"ports"`
	InitialScore      int            `json:"initialScore"`
	MinScore          int            `json:"minScore"`
	DisplayOrder      int            `json:"displayOrder"`      // 显示顺序
	HintCount         int            `json:"hintCount"`         // 提示总数
	HintReleasedCount int            `json:"hintReleasedCount"` // 已发布提示数
	Status            string         `json:"status"`
	ReleaseTime       *string        `json:"releaseTime"`       // 定时放题时间
	CreatedAt         string         `json:"createdAt"`
	UpdatedAt         string         `json:"updatedAt"`
}

// HandleListContestChallenges 获取比赛的题目关联列表
func HandleListContestChallenges(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	rows, err := db.Query(`
		SELECT cc.id, cc.contest_id, cc.question_id, q.title, q.type, q.category_id, cat.name,
			cc.difficulty, q.description, q.flag, q.flag_type, q.docker_image, q.attachment_url, q.ports,
			cc.initial_score, COALESCE(cc.min_score, 17), COALESCE(cc.display_order, 0),
			cc.status, cc.release_time, cc.created_at, cc.updated_at,
			(SELECT COUNT(*) FROM contest_challenge_hints WHERE challenge_id = cc.id) as hint_count,
			(SELECT COUNT(*) FROM contest_challenge_hints WHERE challenge_id = cc.id AND released = true) as hint_released_count
		FROM contest_challenges cc
		JOIN question_bank q ON cc.question_id = q.id
		LEFT JOIN categories cat ON q.category_id = cat.id
		WHERE cc.contest_id = $1
		ORDER BY CASE WHEN cc.display_order = 0 THEN 999999 ELSE cc.display_order END, cc.id`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}
	defer rows.Close()

	var challenges []ContestChallenge
	for rows.Next() {
		var cc ContestChallenge
		var createdAt, updatedAt time.Time
		var releaseTime sql.NullTime
		if err := rows.Scan(&cc.ID, &cc.ContestID, &cc.QuestionID, &cc.Title, &cc.Type, &cc.CategoryID, &cc.CategoryName,
			&cc.Difficulty, &cc.Description, &cc.Flag, &cc.FlagType, &cc.DockerImage, &cc.Attachment, &cc.Ports,
			&cc.InitialScore, &cc.MinScore, &cc.DisplayOrder, &cc.Status, &releaseTime, &createdAt, &updatedAt,
			&cc.HintCount, &cc.HintReleasedCount); err != nil {
			continue
		}
		cc.CreatedAt = createdAt.Format(time.RFC3339)
		cc.UpdatedAt = updatedAt.Format(time.RFC3339)
		if releaseTime.Valid {
			// 将数据库时间当作本地时间处理，避免时区误差
			loc, _ := time.LoadLocation("Asia/Shanghai")
			localTime := time.Date(
				releaseTime.Time.Year(), releaseTime.Time.Month(), releaseTime.Time.Day(),
				releaseTime.Time.Hour(), releaseTime.Time.Minute(), releaseTime.Time.Second(),
				releaseTime.Time.Nanosecond(), loc)
			rt := localTime.Format(time.RFC3339)
			cc.ReleaseTime = &rt
		}
		challenges = append(challenges, cc)
	}

	if challenges == nil {
		challenges = []ContestChallenge{}
	}

	c.JSON(http.StatusOK, challenges)
}

// AddContestChallengeRequest 添加比赛题目请求
type AddContestChallengeRequest struct {
	QuestionIDs  []int64 `json:"questionIds"`
	InitialScore int     `json:"initialScore"`
	MinScore     int     `json:"minScore"`
	Difficulty   int     `json:"difficulty"`
}

// HandleAddContestChallenge 添加比赛题目
func HandleAddContestChallenge(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	var req AddContestChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if len(req.QuestionIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_QUESTIONS_SELECTED"})
		return
	}

	// 默认值
	if req.InitialScore == 0 {
		req.InitialScore = 500
	}
	if req.MinScore == 0 {
		req.MinScore = 17
	}
	if req.Difficulty == 0 {
		req.Difficulty = 5
	}

	var added int
	for _, qid := range req.QuestionIDs {
		// 检查是否已存在
		var exists bool
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM contest_challenges WHERE contest_id = $1 AND question_id = $2)", contestID, qid).Scan(&exists)
		if exists {
			continue
		}

		_, err := db.Exec(`
			INSERT INTO contest_challenges (contest_id, question_id, initial_score, min_score, difficulty, status)
			VALUES ($1, $2, $3, $4, $5, 'hidden')`,
			contestID, qid, req.InitialScore, req.MinScore, req.Difficulty)
		if err == nil {
			added++
		}
	}

	c.JSON(http.StatusCreated, gin.H{"added": added, "message": "添加成功"})
}

// UpdateContestChallengeRequest 更新比赛题目请求
type UpdateContestChallengeRequest struct {
	InitialScore int     `json:"initialScore"`
	MinScore     int     `json:"minScore"`
	Difficulty   int     `json:"difficulty"`
	Status       string  `json:"status"`
	ReleaseTime  *string `json:"releaseTime"` // 定时放题时间，格式 RFC3339
}

// HandleUpdateContestChallenge 更新比赛题目
func HandleUpdateContestChallenge(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 使用 map 解析以支持区分"未传递"和"传递null"
	var rawReq map[string]interface{}
	if err := c.ShouldBindJSON(&rawReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 提取字段
	var req UpdateContestChallengeRequest
	if v, ok := rawReq["initialScore"].(float64); ok {
		req.InitialScore = int(v)
	}
	if v, ok := rawReq["minScore"].(float64); ok {
		req.MinScore = int(v)
	}
	if v, ok := rawReq["difficulty"].(float64); ok {
		req.Difficulty = int(v)
	}
	if v, ok := rawReq["status"].(string); ok {
		req.Status = v
	}

	// 验证状态
	if req.Status != "" && req.Status != "hidden" && req.Status != "public" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
		return
	}

	// 先获取当前状态和题目信息（用于公告）
	var oldStatus string
	var contestID int64
	var challengeName string
	db.QueryRow(`SELECT cc.status, cc.contest_id, q.title FROM contest_challenges cc 
		JOIN question_bank q ON cc.question_id = q.id WHERE cc.id = $1`, id).Scan(&oldStatus, &contestID, &challengeName)

	// 构建动态更新SQL
	updates := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []interface{}{}
	argIndex := 1

	if req.InitialScore > 0 {
		updates = append(updates, fmt.Sprintf("initial_score = $%d", argIndex))
		args = append(args, req.InitialScore)
		argIndex++
	}
	if req.MinScore > 0 {
		updates = append(updates, fmt.Sprintf("min_score = $%d", argIndex))
		args = append(args, req.MinScore)
		argIndex++
	}
	if req.Difficulty > 0 {
		updates = append(updates, fmt.Sprintf("difficulty = $%d", argIndex))
		args = append(args, req.Difficulty)
		argIndex++
	}
	// 支持更新显示顺序
	if v, ok := rawReq["displayOrder"]; ok {
		if order, ok := v.(float64); ok {
			updates = append(updates, fmt.Sprintf("display_order = $%d", argIndex))
			args = append(args, int(order))
			argIndex++
		}
	}
	if req.Status != "" {
		updates = append(updates, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, req.Status)
		argIndex++
	}

	// 处理 releaseTime：区分"未传递"和"传递null"
	if _, exists := rawReq["releaseTime"]; exists {
		if rawReq["releaseTime"] == nil {
			// 明确传递null，清除定时放题
			updates = append(updates, "release_time = NULL")
		} else if rtStr, ok := rawReq["releaseTime"].(string); ok && rtStr != "" {
			// 尝试多种时间格式
			var parsedTime time.Time
			var parseErr error
			// 尝试 RFC3339
			if parsedTime, parseErr = time.Parse(time.RFC3339, rtStr); parseErr != nil {
				// 尝试本地时间格式 YYYY-MM-DDTHH:mm:ss
				if parsedTime, parseErr = time.ParseInLocation("2006-01-02T15:04:05", rtStr, time.Local); parseErr != nil {
					// 尝试本地时间格式 YYYY-MM-DDTHH:mm
					if parsedTime, parseErr = time.ParseInLocation("2006-01-02T15:04", rtStr, time.Local); parseErr != nil {
						c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_RELEASE_TIME"})
						return
					}
				}
			}
			updates = append(updates, fmt.Sprintf("release_time = $%d", argIndex))
			args = append(args, parsedTime)
			argIndex++
		}
	}

	// 添加 WHERE 条件
	args = append(args, id)
	query := fmt.Sprintf("UPDATE contest_challenges SET %s WHERE id = $%d",
		joinStrings(updates, ", "), argIndex)

	result, err := db.Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	// 状态变更时自动发布公告
	if req.Status != "" && req.Status != oldStatus && AnnounceChallenge != nil && contestID > 0 {
		if req.Status == "public" {
			go AnnounceChallenge(db, contestID, challengeName, "open")
			// 题目变为公开时，为所有已审核通过的队伍生成Flag
			if GenerateTeamChallengeFlag != nil {
				go GenerateTeamChallengeFlag(db, fmt.Sprintf("%d", contestID), id)
			}
		} else if req.Status == "hidden" {
			go AnnounceChallenge(db, contestID, challengeName, "close")
		}
	}

	// 分数配置变更时，通过WebSocket广播更新所有前端
	if contestID > 0 && (req.InitialScore > 0 || req.MinScore > 0 || req.Difficulty > 0) {
		contestIDStr := fmt.Sprintf("%d", contestID)
		go func() {
			data := monitor.GetMonitorDataForBroadcast(db, contestIDStr)
			monitor.BroadcastMonitorUpdate(contestIDStr, data)
		}()
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}

// joinStrings 连接字符串切片
func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// HandleRemoveContestChallenge 从比赛移除题目
func HandleRemoveContestChallenge(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 先删除该题目关联的所有 Flag
	_, _ = db.Exec("DELETE FROM team_challenge_flags WHERE challenge_id = $1", id)

	result, err := db.Exec("DELETE FROM contest_challenges WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// UpdateHintRequest 更新题目提示请求
type UpdateHintRequest struct {
	Hint    string `json:"hint"`
	Release bool   `json:"release"` // 是否立即发布提示
}

// HandleUpdateChallengeHint 更新题目提示
func HandleUpdateChallengeHint(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var req UpdateHintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 获取题目信息（用于公告）
	var contestID int64
	var challengeName string
	var oldHintReleased bool
	err := db.QueryRow(`SELECT cc.contest_id, q.title, COALESCE(cc.hint_released, false) FROM contest_challenges cc 
		JOIN question_bank q ON cc.question_id = q.id WHERE cc.id = $1`, id).Scan(&contestID, &challengeName, &oldHintReleased)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	// 更新提示
	_, err = db.Exec(`UPDATE contest_challenges SET hint = $1, hint_released = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $3`,
		req.Hint, req.Release, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	// 如果是新发布提示（之前未发布，现在发布），自动发布公告
	if req.Release && !oldHintReleased && AnnounceChallenge != nil && contestID > 0 {
		go AnnounceChallenge(db, contestID, challengeName, "hint")
	}

	c.JSON(http.StatusOK, gin.H{"message": "提示更新成功"})
}

// HandleReleaseChallengeHint 发布题目提示（单独操作）
func HandleReleaseChallengeHint(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 获取题目信息
	var contestID int64
	var challengeName string
	var hint string
	var oldHintReleased bool
	err := db.QueryRow(`SELECT cc.contest_id, q.title, COALESCE(cc.hint, ''), COALESCE(cc.hint_released, false) 
		FROM contest_challenges cc JOIN question_bank q ON cc.question_id = q.id WHERE cc.id = $1`, id).Scan(&contestID, &challengeName, &hint, &oldHintReleased)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	if hint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_HINT", "message": "请先设置提示内容"})
		return
	}

	if oldHintReleased {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ALREADY_RELEASED", "message": "提示已经发布过了"})
		return
	}

	// 更新为已发布
	_, err = db.Exec(`UPDATE contest_challenges SET hint_released = true, updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	// 发布公告
	if AnnounceChallenge != nil && contestID > 0 {
		go AnnounceChallenge(db, contestID, challengeName, "hint")
	}

	c.JSON(http.StatusOK, gin.H{"message": "提示已发布"})
}

// ========== 多提示支持 ==========

// ChallengeHint 题目提示结构
type ChallengeHint struct {
	ID          int64  `json:"id"`
	ChallengeID int64  `json:"challengeId"`
	Content     string `json:"content"`
	Released    bool   `json:"released"`
	ReleasedAt  string `json:"releasedAt,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

// HandleListChallengeHints 获取题目的所有提示（管理端）
func HandleListChallengeHints(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("id")

	rows, err := db.Query(`
		SELECT id, challenge_id, content, released, released_at, created_at
		FROM contest_challenge_hints
		WHERE challenge_id = $1
		ORDER BY created_at ASC`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	var hints []ChallengeHint
	for rows.Next() {
		var h ChallengeHint
		var releasedAt, createdAt sql.NullTime
		if err := rows.Scan(&h.ID, &h.ChallengeID, &h.Content, &h.Released, &releasedAt, &createdAt); err != nil {
			continue
		}
		if releasedAt.Valid {
			h.ReleasedAt = releasedAt.Time.Format(time.RFC3339)
		}
		if createdAt.Valid {
			h.CreatedAt = createdAt.Time.Format(time.RFC3339)
		}
		hints = append(hints, h)
	}

	if hints == nil {
		hints = []ChallengeHint{}
	}
	c.JSON(http.StatusOK, hints)
}

// AddHintRequest 添加提示请求
type AddHintRequest struct {
	Content string `json:"content"`
}

// HandleAddChallengeHint 添加新提示
func HandleAddChallengeHint(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("id")

	var req AddHintRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var id int64
	err := db.QueryRow(`INSERT INTO contest_challenge_hints (challenge_id, content) VALUES ($1, $2) RETURNING id`,
		challengeID, req.Content).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "提示添加成功"})
}

// HandleDeleteChallengeHint 删除提示
func HandleDeleteChallengeHint(c *gin.Context, db *sql.DB) {
	hintID := c.Param("hintId")

	result, err := db.Exec(`DELETE FROM contest_challenge_hints WHERE id = $1`, hintID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "提示已删除"})
}

// HandleReleaseSingleHint 发布单个提示
func HandleReleaseSingleHint(c *gin.Context, db *sql.DB) {
	hintID := c.Param("hintId")

	// 获取提示信息
	var challengeID int64
	var released bool
	err := db.QueryRow(`SELECT challenge_id, released FROM contest_challenge_hints WHERE id = $1`, hintID).Scan(&challengeID, &released)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	if released {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ALREADY_RELEASED", "message": "该提示已经发布过了"})
		return
	}

	// 更新为已发布
	_, err = db.Exec(`UPDATE contest_challenge_hints SET released = true, released_at = CURRENT_TIMESTAMP WHERE id = $1`, hintID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	// 获取题目名称和比赛ID用于发布公告
	var contestID int64
	var challengeName string
	db.QueryRow(`SELECT cc.contest_id, q.title FROM contest_challenges cc 
		JOIN question_bank q ON cc.question_id = q.id WHERE cc.id = $1`, challengeID).Scan(&contestID, &challengeName)

	// 发布公告
	if AnnounceChallenge != nil && contestID > 0 {
		go AnnounceChallenge(db, contestID, challengeName, "hint")
	}

	c.JSON(http.StatusOK, gin.H{"message": "提示已发布"})
}

// HandleGetReleasedHints 获取已发布的提示（用户端）
func HandleGetReleasedHints(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("challengeId")

	rows, err := db.Query(`
		SELECT id, content, released_at
		FROM contest_challenge_hints
		WHERE challenge_id = $1 AND released = true
		ORDER BY released_at ASC`, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	type ReleasedHint struct {
		ID         int64  `json:"id"`
		Content    string `json:"content"`
		ReleasedAt string `json:"releasedAt"`
	}

	var hints []ReleasedHint
	for rows.Next() {
		var h ReleasedHint
		var releasedAt sql.NullTime
		if err := rows.Scan(&h.ID, &h.Content, &releasedAt); err != nil {
			continue
		}
		if releasedAt.Valid {
			h.ReleasedAt = releasedAt.Time.Format(time.RFC3339)
		}
		hints = append(hints, h)
	}

	if hints == nil {
		hints = []ReleasedHint{}
	}
	c.JSON(http.StatusOK, hints)
}

// HandleGetChallengeHintCount 获取题目提示数量（用于管理端列表显示）
func HandleGetChallengeHintCount(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("id")

	var total, released int
	db.QueryRow(`SELECT COUNT(*) FROM contest_challenge_hints WHERE challenge_id = $1`, challengeID).Scan(&total)
	db.QueryRow(`SELECT COUNT(*) FROM contest_challenge_hints WHERE challenge_id = $1 AND released = true`, challengeID).Scan(&released)

	c.JSON(http.StatusOK, gin.H{"total": total, "released": released})
}

// ========== 定时放题调度器 ==========

// StartScheduledReleaseChecker 启动定时放题检查器
func StartScheduledReleaseChecker(db *sql.DB) {
	ticker := time.NewTicker(1 * time.Second) // 每1秒检查一次，误差最多1秒
	go func() {
		for {
			<-ticker.C
			checkAndReleaseScheduledChallenges(db)
		}
	}()
	fmt.Println("[定时放题] 检查器已启动，每1秒检查一次")
}

// checkAndReleaseScheduledChallenges 检查并释放到期的题目
func checkAndReleaseScheduledChallenges(db *sql.DB) {
	now := time.Now()

	// 查找所有已到时间但状态仍为hidden的题目
	rows, err := db.Query(`
		SELECT cc.id, cc.contest_id, q.title
		FROM contest_challenges cc
		JOIN question_bank q ON cc.question_id = q.id
		WHERE cc.status = 'hidden'
		  AND cc.release_time IS NOT NULL
		  AND cc.release_time <= $1`, now)
	if err != nil {
		fmt.Printf("[定时放题] 查询失败: %v\n", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var challengeID, contestID int64
		var title string
		if err := rows.Scan(&challengeID, &contestID, &title); err != nil {
			continue
		}

		// 更新状态为 public，并清除 release_time
		_, err := db.Exec(`UPDATE contest_challenges SET status = 'public', release_time = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = $1`, challengeID)
		if err != nil {
			fmt.Printf("[定时放题] 更新题目 %d 失败: %v\n", challengeID, err)
			continue
		}

		fmt.Printf("[定时放题] 题目 '%s' (ID:%d) 已自动放出\n", title, challengeID)

		// 发布公告
		if AnnounceChallenge != nil {
			go AnnounceChallenge(db, contestID, title, "open")
		}

		// 为所有已审核队伍生成Flag
		if GenerateTeamChallengeFlag != nil {
			go GenerateTeamChallengeFlag(db, fmt.Sprintf("%d", contestID), fmt.Sprintf("%d", challengeID))
		}
	}
}

// ========== 批量更新题目显示顺序 ==========

// BatchUpdateOrderRequest 批量更新顺序请求
type BatchUpdateOrderRequest struct {
	Orders []ChallengeOrder `json:"orders"`
}

// ChallengeOrder 题目顺序
type ChallengeOrder struct {
	ID           int64 `json:"id"`
	DisplayOrder int   `json:"displayOrder"`
}

// HandleBatchUpdateChallengeOrder 批量更新题目显示顺序
func HandleBatchUpdateChallengeOrder(c *gin.Context, db *sql.DB) {
	var req BatchUpdateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if len(req.Orders) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_ORDERS"})
		return
	}

	// 批量更新
	var updated int
	for _, order := range req.Orders {
		result, err := db.Exec(`UPDATE contest_challenges SET display_order = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`,
			order.DisplayOrder, order.ID)
		if err == nil {
			if rows, _ := result.RowsAffected(); rows > 0 {
				updated++
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"updated": updated, "message": "更新成功"})
}
