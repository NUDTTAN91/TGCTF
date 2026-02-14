// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package question

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Challenge 比赛题目
type Challenge struct {
	ID            int64  `json:"id"`
	ContestID     int64  `json:"contestId"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Type          string `json:"type"`
	Description   string `json:"description"`
	Score         int    `json:"score"`
	Flag          string `json:"flag,omitempty"`
	Status        string `json:"status"`
	AttachmentURL string `json:"attachmentUrl,omitempty"`
	DockerImage   string `json:"dockerImage,omitempty"`
	Ports         string `json:"ports,omitempty"`
	CreatedAt     string `json:"createdAt,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

// PublicChallenge 公开题目信息（不包含flag）
type PublicChallenge struct {
	ID                int64   `json:"id"`
	ContestID         int64   `json:"contestId"`
	QuestionID        int64   `json:"questionId"`
	Name              string  `json:"name"`
	Category          string  `json:"category"`
	Type              string  `json:"type"`
	Description       string  `json:"description"`
	Score             int     `json:"score"`
	Status            string  `json:"status"`
	DisplayOrder      int     `json:"displayOrder"`
	AttachmentURL     *string `json:"attachmentUrl"`
	AttachmentType    string  `json:"attachmentType"`
	CreatedAt         string  `json:"createdAt,omitempty"`
	UpdatedAt         string  `json:"updatedAt,omitempty"`
	// AWD-F 模式特有字段
	AttackInterval    int `json:"attackInterval,omitempty"`    // 攻击间隔(秒)
	DefenseScore      int `json:"defenseScore,omitempty"`      // 每轮防守得分
	NextAttackSeconds int `json:"nextAttackSeconds,omitempty"` // 距离下次攻击剩余秒数
}

// CreateChallengeRequest 创建题目请求
type CreateChallengeRequest struct {
	Name          string `json:"name" binding:"required"`
	Category      string `json:"category" binding:"required"`
	Description   string `json:"description"`
	Score         int    `json:"score"`
	Flag          string `json:"flag"`
	Status        string `json:"status"`
	AttachmentURL string `json:"attachmentUrl"`
}

// UpdateChallengeRequest 更新题目请求
type UpdateChallengeRequest struct {
	Name          string `json:"name"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Score         int    `json:"score"`
	Flag          string `json:"flag"`
	Status        string `json:"status"`
	AttachmentURL string `json:"attachmentUrl"`
}

// HandleListChallenges 获取比赛的题目列表
func HandleListChallenges(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 获取每道题的当前解题人数
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

	rows, err := db.Query(`
		SELECT cc.id, cc.contest_id, q.title, cat.name as category, q.type, COALESCE(q.description,''), 
		       cc.initial_score, cc.min_score, cc.difficulty, COALESCE(q.flag,''), cc.status, COALESCE(q.attachment_url,''),
		       COALESCE(q.docker_image,''), COALESCE(q.ports,''), cc.created_at, cc.updated_at
		FROM contest_challenges cc
		JOIN question_bank q ON cc.question_id = q.id
		LEFT JOIN categories cat ON q.category_id = cat.id
		WHERE cc.contest_id = $1
		ORDER BY cat.name, cc.id`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()

	var challenges []Challenge
	for rows.Next() {
		var ch Challenge
		var initialScore, minScore, difficulty int
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&ch.ID, &ch.ContestID, &ch.Name, &ch.Category, &ch.Type, &ch.Description,
			&initialScore, &minScore, &difficulty, &ch.Flag, &ch.Status, &ch.AttachmentURL, &ch.DockerImage, &ch.Ports, &createdAt, &updatedAt); err != nil {
			fmt.Printf("[ERROR] scan challenge: %v\n", err)
			continue
		}
		// 计算动态分数
		solveCount := challengeSolveCountMap[ch.ID]
		ch.Score = calculateDynamicScore(initialScore, minScore, difficulty, solveCount)
		if createdAt.Valid {
			ch.CreatedAt = createdAt.Time.Format("2006-01-02 15:04:05")
		}
		if updatedAt.Valid {
			ch.UpdatedAt = updatedAt.Time.Format("2006-01-02 15:04:05")
		}
		challenges = append(challenges, ch)
	}

	c.JSON(http.StatusOK, challenges)
}

// HandleCreateChallenge 创建题目
func HandleCreateChallenge(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	// 默认值
	if req.Score == 0 {
		req.Score = 500
	}
	if req.Status == "" {
		req.Status = "hidden"
	}

	// 验证分类
	validCategories := map[string]bool{"WEB": true, "PWN": true, "REVERSE": true, "CRYPTO": true, "MISC": true}
	if !validCategories[req.Category] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分类，必须是 WEB/PWN/REVERSE/CRYPTO/MISC"})
		return
	}

	var id int64
	err := db.QueryRow(`
		INSERT INTO challenges (contest_id, name, category, description, score, flag, status, attachment_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		contestID, req.Name, req.Category, req.Description, req.Score, req.Flag, req.Status, NullIfEmpty(req.AttachmentURL),
	).Scan(&id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "创建成功"})
}

// HandleGetChallenge 获取单个题目
func HandleGetChallenge(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var ch Challenge
	var createdAt, updatedAt sql.NullTime
	err := db.QueryRow(`
		SELECT id, contest_id, name, category, COALESCE(description,''), score, flag, status, 
		       attachment_url, created_at, updated_at
		FROM challenges WHERE id = $1`, id).Scan(
		&ch.ID, &ch.ContestID, &ch.Name, &ch.Category, &ch.Description,
		&ch.Score, &ch.Flag, &ch.Status, &ch.AttachmentURL, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}

	if createdAt.Valid {
		ch.CreatedAt = createdAt.Time.Format("2006-01-02 15:04:05")
	}
	if updatedAt.Valid {
		ch.UpdatedAt = updatedAt.Time.Format("2006-01-02 15:04:05")
	}

	c.JSON(http.StatusOK, ch)
}

// HandleUpdateChallenge 更新题目
func HandleUpdateChallenge(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var req UpdateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	// 验证分类
	if req.Category != "" {
		validCategories := map[string]bool{"WEB": true, "PWN": true, "REVERSE": true, "CRYPTO": true, "MISC": true}
		if !validCategories[req.Category] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分类"})
			return
		}
	}

	// 验证状态
	if req.Status != "" && req.Status != "hidden" && req.Status != "public" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的状态，必须是 hidden 或 public"})
		return
	}

	result, err := db.Exec(`
		UPDATE challenges SET 
			name = COALESCE(NULLIF($1,''), name),
			category = COALESCE(NULLIF($2,''), category),
			description = COALESCE(NULLIF($3,''), description),
			score = CASE WHEN $4 > 0 THEN $4 ELSE score END,
			flag = COALESCE(NULLIF($5,''), flag),
			status = COALESCE(NULLIF($6,''), status),
			attachment_url = CASE WHEN $7 != '' THEN $7 ELSE attachment_url END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $8`,
		req.Name, req.Category, req.Description, req.Score, req.Flag, req.Status, req.AttachmentURL, id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}

// HandleDeleteChallenge 删除题目
func HandleDeleteChallenge(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	result, err := db.Exec("DELETE FROM challenges WHERE id = $1", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// HandlePublicChallenges 获取比赛的公开题目列表（不返回flag）
// 所有用户（包括超级管理员）都只能看到 public 状态的题目，保持一致体验
func HandlePublicChallenges(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	// 检查比赛模式
	var contestMode string
	err := db.QueryRow("SELECT mode FROM contests WHERE id = $1", contestID).Scan(&contestMode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "比赛不存在"})
		return
	}

	// 获取每道题的当前解题人数
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

	var challenges []PublicChallenge

	if contestMode == "awd-f" {
		// AWD-F 模式：查询 contest_challenges_awdf 和 question_bank_awdf
		rows, err := db.Query(`
			SELECT cc.id, cc.contest_id, cc.question_id, q.title, cat.name as category, 'dynamic' as type, COALESCE(q.description,''), 
			       cc.initial_score, cc.min_score, 5 as difficulty, cc.status, COALESCE(cc.display_order, 0), '' as attachment_url, 'url' as attachment_type, cc.created_at, cc.updated_at,
			       cc.attack_interval, cc.defense_score
			FROM contest_challenges_awdf cc
			JOIN question_bank_awdf q ON cc.question_id = q.id
			LEFT JOIN categories cat ON q.category_id = cat.id
			WHERE cc.contest_id = $1 AND cc.status = 'public'
			ORDER BY CASE WHEN cc.display_order = 0 THEN 999999 ELSE cc.display_order END, cc.id`, contestID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		defer rows.Close()

		// 获取每个题目距离上次攻击已过秒数（在数据库层计算，避免时区问题）
		elapsedSecondsMap := make(map[int64]int)
		attackRows, _ := db.Query(`
			SELECT challenge_id, EXTRACT(EPOCH FROM (NOW() - MAX(executed_at)))::int as elapsed_seconds
			FROM awdf_exp_results 
			WHERE contest_id = $1 
			GROUP BY challenge_id`, contestID)
		if attackRows != nil {
			for attackRows.Next() {
				var cid int64
				var elapsedSeconds int
				attackRows.Scan(&cid, &elapsedSeconds)
				elapsedSecondsMap[cid] = elapsedSeconds
			}
			attackRows.Close()
		}

		for rows.Next() {
			var ch PublicChallenge
			var initialScore, minScore, difficulty int
			var createdAt, updatedAt sql.NullTime
			if err := rows.Scan(&ch.ID, &ch.ContestID, &ch.QuestionID, &ch.Name, &ch.Category, &ch.Type, &ch.Description,
				&initialScore, &minScore, &difficulty, &ch.Status, &ch.DisplayOrder, &ch.AttachmentURL, &ch.AttachmentType, &createdAt, &updatedAt,
				&ch.AttackInterval, &ch.DefenseScore); err != nil {
				continue
			}
			solveCount := challengeSolveCountMap[ch.ID]
			ch.Score = calculateDynamicScore(initialScore, minScore, difficulty, solveCount)
			if createdAt.Valid {
				ch.CreatedAt = createdAt.Time.Format("2006-01-02 15:04:05")
			}
			if updatedAt.Valid {
				ch.UpdatedAt = updatedAt.Time.Format("2006-01-02 15:04:05")
			}
			// 计算距离下次攻击的剩余秒数
			if elapsedSeconds, ok := elapsedSecondsMap[ch.ID]; ok {
				remaining := ch.AttackInterval - elapsedSeconds
				if remaining > 0 {
					ch.NextAttackSeconds = remaining
				} else {
					ch.NextAttackSeconds = 0 // 已到时间，即将攻击
				}
			} else {
				// 还没有执行过攻击，默认显示满间隔
				ch.NextAttackSeconds = ch.AttackInterval
			}
			challenges = append(challenges, ch)
		}
	} else {
		// Jeopardy/AWD 模式：查询 contest_challenges 和 question_bank
		rows, err := db.Query(`
			SELECT cc.id, cc.contest_id, cc.question_id, q.title, cat.name as category, q.type, COALESCE(q.description,''), 
			       cc.initial_score, cc.min_score, cc.difficulty, cc.status, COALESCE(cc.display_order, 0), COALESCE(q.attachment_url,''), COALESCE(q.attachment_type,'url'), cc.created_at, cc.updated_at
			FROM contest_challenges cc
			JOIN question_bank q ON cc.question_id = q.id
			LEFT JOIN categories cat ON q.category_id = cat.id
			WHERE cc.contest_id = $1 AND cc.status = 'public'
			ORDER BY CASE WHEN cc.display_order = 0 THEN 999999 ELSE cc.display_order END, cc.id`, contestID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var ch PublicChallenge
			var initialScore, minScore, difficulty int
			var createdAt, updatedAt sql.NullTime
			if err := rows.Scan(&ch.ID, &ch.ContestID, &ch.QuestionID, &ch.Name, &ch.Category, &ch.Type, &ch.Description,
				&initialScore, &minScore, &difficulty, &ch.Status, &ch.DisplayOrder, &ch.AttachmentURL, &ch.AttachmentType, &createdAt, &updatedAt); err != nil {
				continue
			}
			solveCount := challengeSolveCountMap[ch.ID]
			ch.Score = calculateDynamicScore(initialScore, minScore, difficulty, solveCount)
			if createdAt.Valid {
				ch.CreatedAt = createdAt.Time.Format("2006-01-02 15:04:05")
			}
			if updatedAt.Valid {
				ch.UpdatedAt = updatedAt.Time.Format("2006-01-02 15:04:05")
			}
			challenges = append(challenges, ch)
		}
	}

	c.JSON(http.StatusOK, challenges)
}

// calculateDynamicScore 计算题目当前动态分数 - 使用指数衰减公式
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
