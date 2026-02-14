// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// FlagGeneratorFunc Flag生成函数类型
type FlagGeneratorFunc func(db *sql.DB, contestID, challengeID string)

// GenerateTeamChallengeFlag 全局变量，用于注入Flag生成函数
var GenerateTeamChallengeFlag FlagGeneratorFunc

// AWDFContestChallenge AWD-F比赛题目关联
type AWDFContestChallenge struct {
	ID             int64   `json:"id"`
	ContestID      int64   `json:"contestId"`
	QuestionID     int64   `json:"questionId"`
	Title          string  `json:"title"`
	CategoryID     int64   `json:"categoryId"`
	CategoryName   string  `json:"categoryName"`
	CategoryColor  string  `json:"categoryColor"`
	Description    *string `json:"description"`
	DockerImage    string  `json:"dockerImage"`
	InitialScore   int     `json:"initialScore"`
	MinScore       int     `json:"minScore"`
	DefenseScore   int     `json:"defenseScore"`
	AttackInterval int     `json:"attackInterval"`
	Status         string  `json:"status"`
	SolveCount     int     `json:"solveCount"`
	DisplayOrder   int     `json:"displayOrder"`
	CreatedAt      string  `json:"createdAt"`
}

// HandleListAWDFContestChallenges 获取比赛的AWD-F题目列表
func HandleListAWDFContestChallenges(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	rows, err := db.Query(`
		SELECT cc.id, cc.contest_id, cc.question_id, q.title, q.category_id, 
			cat.name, cat.glow_color, q.description, q.docker_image,
			cc.initial_score, cc.min_score, cc.defense_score, cc.attack_interval,
			cc.status, COALESCE(cc.display_order, 0), cc.created_at,
			(SELECT COUNT(*) FROM submissions s WHERE s.challenge_id = cc.id AND s.is_correct = true) as solve_count
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		LEFT JOIN categories cat ON q.category_id = cat.id
		WHERE cc.contest_id = $1
		ORDER BY COALESCE(cc.display_order, 999), cc.id
	`, contestID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}
	defer rows.Close()

	var challenges []AWDFContestChallenge
	for rows.Next() {
		var ch AWDFContestChallenge
		var catName, catColor sql.NullString
		var createdAt time.Time

		err := rows.Scan(
			&ch.ID, &ch.ContestID, &ch.QuestionID, &ch.Title, &ch.CategoryID,
			&catName, &catColor, &ch.Description, &ch.DockerImage,
			&ch.InitialScore, &ch.MinScore, &ch.DefenseScore, &ch.AttackInterval,
			&ch.Status, &ch.DisplayOrder, &createdAt, &ch.SolveCount,
		)
		if err != nil {
			continue
		}
		if catName.Valid {
			ch.CategoryName = catName.String
		}
		if catColor.Valid {
			ch.CategoryColor = catColor.String
		}
		ch.CreatedAt = createdAt.Format(time.RFC3339)
		challenges = append(challenges, ch)
	}

	if challenges == nil {
		challenges = []AWDFContestChallenge{}
	}

	c.JSON(http.StatusOK, challenges)
}

// HandleAddAWDFContestChallenge 从AWD-F题库添加题目到比赛
func HandleAddAWDFContestChallenge(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	var req struct {
		QuestionID     int64 `json:"questionId" binding:"required"`
		InitialScore   int   `json:"initialScore"`
		MinScore       int   `json:"minScore"`
		DefenseScore   int   `json:"defenseScore"`
		AttackInterval int   `json:"attackInterval"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 验证比赛是否是AWD-F模式
	var contestMode string
	err := db.QueryRow("SELECT mode FROM contests WHERE id = $1", contestID).Scan(&contestMode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CONTEST_NOT_FOUND"})
		return
	}
	if contestMode != "awd-f" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AWDF_CONTEST", "message": "该比赛不是AWD-F模式，请选择Jeopardy题库"})
		return
	}

	// 验证题目存在于AWD-F题库
	var exists int
	err = db.QueryRow("SELECT 1 FROM question_bank_awdf WHERE id = $1", req.QuestionID).Scan(&exists)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "QUESTION_NOT_FOUND", "message": "题目不存在于AWD-F题库"})
		return
	}

	// 检查是否已添加
	err = db.QueryRow("SELECT 1 FROM contest_challenges_awdf WHERE contest_id = $1 AND question_id = $2", contestID, req.QuestionID).Scan(&exists)
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "ALREADY_EXISTS", "message": "该题目已添加到比赛"})
		return
	}

	// 设置默认值
	if req.InitialScore == 0 {
		req.InitialScore = 500
	}
	if req.MinScore == 0 {
		req.MinScore = 100
	}
	if req.DefenseScore == 0 {
		req.DefenseScore = 100
	}
	if req.AttackInterval == 0 {
		req.AttackInterval = 60
	}

	// 插入关联记录
	var id int64
	err = db.QueryRow(`
		INSERT INTO contest_challenges_awdf (contest_id, question_id, initial_score, min_score, defense_score, attack_interval, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'hidden')
		RETURNING id
	`, contestID, req.QuestionID, req.InitialScore, req.MinScore, req.DefenseScore, req.AttackInterval).Scan(&id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "message": "题目已添加"})
}

// HandleUpdateAWDFContestChallenge 更新AWD-F比赛题目配置
func HandleUpdateAWDFContestChallenge(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("id")

	var req struct {
		InitialScore   *int    `json:"initialScore"`
		MinScore       *int    `json:"minScore"`
		DefenseScore   *int    `json:"defenseScore"`
		AttackInterval *int    `json:"attackInterval"`
		Status         *string `json:"status"`
		DisplayOrder   *int    `json:"displayOrder"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 获取当前状态和比赛ID（用于判断是否需要生成Flag）
	var oldStatus string
	var contestID int64
	db.QueryRow(`SELECT status, contest_id FROM contest_challenges_awdf WHERE id = $1`, challengeID).Scan(&oldStatus, &contestID)

	// 构建更新语句
	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argIndex := 1

	if req.InitialScore != nil {
		setClauses = append(setClauses, fmt.Sprintf("initial_score = $%d", argIndex))
		args = append(args, *req.InitialScore)
		argIndex++
	}
	if req.MinScore != nil {
		setClauses = append(setClauses, fmt.Sprintf("min_score = $%d", argIndex))
		args = append(args, *req.MinScore)
		argIndex++
	}
	if req.DefenseScore != nil {
		setClauses = append(setClauses, fmt.Sprintf("defense_score = $%d", argIndex))
		args = append(args, *req.DefenseScore)
		argIndex++
	}
	if req.AttackInterval != nil {
		setClauses = append(setClauses, fmt.Sprintf("attack_interval = $%d", argIndex))
		args = append(args, *req.AttackInterval)
		argIndex++
	}
	if req.Status != nil {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, *req.Status)
		argIndex++
	}
	if req.DisplayOrder != nil {
		setClauses = append(setClauses, fmt.Sprintf("display_order = $%d", argIndex))
		args = append(args, *req.DisplayOrder)
		argIndex++
	}

	query := fmt.Sprintf("UPDATE contest_challenges_awdf SET %s WHERE id = $%d", 
		strings.Join(setClauses, ", "), argIndex)
	args = append(args, challengeID)

	_, err := db.Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	// 如果状态从 hidden 变为 public，为所有已审核队伍生成 Flag 并创建容器
	if req.Status != nil && *req.Status == "public" && oldStatus != "public" && contestID > 0 {
		challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
		if GenerateTeamChallengeFlag != nil {
			go GenerateTeamChallengeFlag(db, fmt.Sprintf("%d", contestID), challengeID)
		}
		// 题目上架时为所有队伍创建容器
		go StartContainersForChallenge(db, contestID, challengeIDInt)
	}

	// 如果状态从 public 变为 hidden，销毁该题目的所有容器
	if req.Status != nil && *req.Status == "hidden" && oldStatus == "public" && contestID > 0 {
		challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
		go StopContainersForChallenge(db, contestID, challengeIDInt)
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}

// HandleRemoveAWDFContestChallenge 从比赛移除AWD-F题目
func HandleRemoveAWDFContestChallenge(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("id")
	challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)

	// 先获取比赛ID，用于销毁容器
	var contestID int64
	db.QueryRow(`SELECT contest_id FROM contest_challenges_awdf WHERE id = $1`, challengeID).Scan(&contestID)

	// 销毁该题目的所有容器
	if contestID > 0 {
		go StopContainersForChallenge(db, contestID, challengeIDInt)
	}

	result, err := db.Exec("DELETE FROM contest_challenges_awdf WHERE id = $1", challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "题目已移除"})
}
