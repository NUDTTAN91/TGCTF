// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package contest

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GenerateFlagsForTeamInContest 队伍审核通过时生成Flag的函数引用
var GenerateFlagsForTeamInContest func(db *sql.DB, contestID string, teamID int64, mode string)

// OnAWDFContestStatusChange AWD-F 比赛状态变更钩子
var OnAWDFContestStatusChange func(db *sql.DB, contestID int64, oldStatus, newStatus, mode string)

// StartContestStatusUpdater 启动比赛状态自动更新定时器
func StartContestStatusUpdater(db *sql.DB) {
	ticker := time.NewTicker(30 * time.Second) // 每30秒检查一次
	go func() {
		for {
			<-ticker.C
			autoUpdateContestStatus(db)
		}
	}()
	log.Println("[Contest] 比赛状态自动更新定时器已启动，每30秒检查一次")
}

// autoUpdateContestStatus 自动更新比赛状态并触发 AWD-F 钩子
func autoUpdateContestStatus(db *sql.DB) {
	// 自动开始：pending -> running
	startRows, err := db.Query(`
		SELECT id, mode FROM contests 
		WHERE status = 'pending' AND start_time <= NOW()
	`)
	if err == nil {
		defer startRows.Close()
		for startRows.Next() {
			var contestID int64
			var mode string
			if err := startRows.Scan(&contestID, &mode); err != nil {
				continue
			}
			// 更新状态
			_, err := db.Exec(`UPDATE contests SET status = 'running', updated_at = NOW() WHERE id = $1`, contestID)
			if err != nil {
				log.Printf("[Contest] 自动开始比赛 %d 失败: %v", contestID, err)
				continue
			}
			log.Printf("[Contest] 比赛 %d 自动开始 (mode=%s)", contestID, mode)
			// 触发 AWD-F 钩子
			if OnAWDFContestStatusChange != nil {
				go OnAWDFContestStatusChange(db, contestID, "pending", "running", mode)
			}
		}
	}

	// 自动结束：running/paused -> ended
	endRows, err := db.Query(`
		SELECT id, mode FROM contests 
		WHERE status IN ('running', 'paused') AND end_time < NOW()
	`)
	if err == nil {
		defer endRows.Close()
		for endRows.Next() {
			var contestID int64
			var mode string
			var oldStatus string
			if err := endRows.Scan(&contestID, &mode); err != nil {
				continue
			}
			// 获取旧状态
			db.QueryRow(`SELECT status FROM contests WHERE id = $1`, contestID).Scan(&oldStatus)
			// 更新状态
			_, err := db.Exec(`UPDATE contests SET status = 'ended', updated_at = NOW() WHERE id = $1`, contestID)
			if err != nil {
				log.Printf("[Contest] 自动结束比赛 %d 失败: %v", contestID, err)
				continue
			}
			log.Printf("[Contest] 比赛 %d 自动结束 (mode=%s)", contestID, mode)
			// 触发 AWD-F 钩子
			if OnAWDFContestStatusChange != nil {
				go OnAWDFContestStatusChange(db, contestID, oldStatus, "ended", mode)
			}
		}
	}
}

// Contest 比赛结构
type Contest struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	Mode             string  `json:"mode"` // jeopardy | awd
	Status           string  `json:"status"`
	CoverImage       *string `json:"coverImage,omitempty"`
	StartTime        string  `json:"startTime"`
	EndTime          string  `json:"endTime"`
	TeamLimit        int     `json:"teamLimit,omitempty"`
	ContainerLimit   int     `json:"containerLimit,omitempty"`
	FlagFormat       string  `json:"flagFormat,omitempty"` // Flag格式，[GUID]为占位符
	DefenseInterval  int     `json:"defenseInterval,omitempty"`  // AWD-F 防守间隔（秒）
	JudgeConcurrency int     `json:"judgeConcurrency,omitempty"` // AWD-F 并发判题数
	CreatedAt        string  `json:"createdAt,omitempty"`
	UpdatedAt        string  `json:"updatedAt,omitempty"`
}

// CreateContestRequest 创建比赛请求
type CreateContestRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Mode        string `json:"mode" binding:"required"`
	CoverImage  string `json:"coverImage"`
	StartTime   string `json:"startTime" binding:"required"`
	EndTime     string `json:"endTime" binding:"required"`
}

// UpdateContestRequest 更新比赛请求
type UpdateContestRequest struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	Mode             string  `json:"mode"`
	Status           string  `json:"status"`
	CoverImage       string  `json:"coverImage"`
	StartTime        string  `json:"startTime"`
	EndTime          string  `json:"endTime"`
	TeamLimit        *int    `json:"teamLimit"`
	ContainerLimit   *int    `json:"containerLimit"`
	FlagFormat       *string `json:"flagFormat"`       // Flag格式，null表示不修改
	DefenseInterval  *int    `json:"defenseInterval"`  // AWD-F 防守间隔
	JudgeConcurrency *int    `json:"judgeConcurrency"` // AWD-F 并发判题数
}

// ContestWithStats 带统计信息的比赛
type ContestWithStats struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Mode           string  `json:"mode"`
	Status         string  `json:"status"`
	CoverImage     *string `json:"coverImage,omitempty"`
	StartTime      string  `json:"startTime"`
	EndTime        string  `json:"endTime"`
	TeamLimit      int     `json:"teamLimit,omitempty"`
	ContainerLimit int     `json:"containerLimit,omitempty"`
	CreatedAt      string  `json:"createdAt,omitempty"`
	UpdatedAt      string  `json:"updatedAt,omitempty"`
	TeamCount      int     `json:"teamCount"`
	ChallengeCount int     `json:"challengeCount"`
	FlagCount      int     `json:"flagCount"`
}

// HandleListContests 获取比赛列表
func HandleListContests(c *gin.Context, db *sql.DB) {
	// 自动更新比赛状态（包括触发 AWD-F 钩子）
	autoUpdateContestStatus(db)

	status := c.Query("status")

	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = db.Query(`
			SELECT id, name, COALESCE(description,''), mode, status, cover_image,
			       start_time, end_time, created_at, updated_at 
			FROM contests WHERE status = $1 ORDER BY start_time DESC`, status)
	} else {
		rows, err = db.Query(`
			SELECT id, name, COALESCE(description,''), mode, status, cover_image,
			       start_time, end_time, created_at, updated_at 
			FROM contests ORDER BY start_time DESC`)
	}

	if err != nil {
		log.Printf("query contests error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var contests []ContestWithStats
	for rows.Next() {
		var ct ContestWithStats
		var startTime, endTime, createdAt, updatedAt time.Time
		if err := rows.Scan(&ct.ID, &ct.Name, &ct.Description, &ct.Mode, &ct.Status, &ct.CoverImage,
			&startTime, &endTime, &createdAt, &updatedAt); err != nil {
			log.Printf("scan contest error: %v", err)
			continue
		}
		ct.StartTime = startTime.Format("2006-01-02 15:04")
		ct.EndTime = endTime.Format("2006-01-02 15:04")
		ct.CreatedAt = createdAt.Format("2006-01-02 15:04")
		ct.UpdatedAt = updatedAt.Format("2006-01-02 15:04")

		// 查询队伍数（已通过审核的队伍）
		db.QueryRow(`SELECT COUNT(*) FROM contest_teams WHERE contest_id = $1 AND status = 'approved'`, ct.ID).Scan(&ct.TeamCount)
		// 查询题目数
		db.QueryRow(`SELECT COUNT(*) FROM contest_challenges WHERE contest_id = $1`, ct.ID).Scan(&ct.ChallengeCount)
		// 查询解题数（Flag提交数）
		db.QueryRow(`SELECT COUNT(*) FROM team_solves WHERE contest_id = $1`, ct.ID).Scan(&ct.FlagCount)

		contests = append(contests, ct)
	}

	if contests == nil {
		contests = []ContestWithStats{}
	}

	c.JSON(http.StatusOK, gin.H{"contests": contests})
}

// HandleCreateContest 创建比赛
func HandleCreateContest(c *gin.Context, db *sql.DB) {
	var req CreateContestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 验证 mode
	if req.Mode != "jeopardy" && req.Mode != "awd" && req.Mode != "awd-f" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_MODE"})
		return
	}

	// 解析时间
	startTime, err := time.ParseInLocation("2006-01-02T15:04", req.StartTime, time.Local)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_START_TIME"})
		return
	}
	endTime, err := time.ParseInLocation("2006-01-02T15:04", req.EndTime, time.Local)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_END_TIME"})
		return
	}

	if endTime.Before(startTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "END_BEFORE_START"})
		return
	}

	var id int64
	var coverImage *string
	if req.CoverImage != "" {
		coverImage = &req.CoverImage
	}
	err = db.QueryRow(`
		INSERT INTO contests (name, description, mode, status, cover_image, start_time, end_time) 
		VALUES ($1, $2, $3, 'pending', $4, $5, $6) RETURNING id`,
		req.Name, req.Description, req.Mode, coverImage, startTime, endTime).Scan(&id)

	if err != nil {
		log.Printf("insert contest error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "CREATED"})
}

// HandleGetContest 获取单个比赛详情
func HandleGetContest(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var ct Contest
	var startTime, endTime, createdAt, updatedAt time.Time
	var flagFormat sql.NullString
	var defenseInterval, judgeConcurrency sql.NullInt64
	err := db.QueryRow(`
		SELECT id, name, COALESCE(description,''), mode, status, cover_image,
		       COALESCE(team_limit, 4), COALESCE(container_limit, 1),
		       COALESCE(flag_format, 'flag{[GUID]}'),
		       defense_interval, judge_concurrency,
		       start_time, end_time, created_at, updated_at 
		FROM contests WHERE id = $1`, id).Scan(
		&ct.ID, &ct.Name, &ct.Description, &ct.Mode, &ct.Status, &ct.CoverImage,
		&ct.TeamLimit, &ct.ContainerLimit, &flagFormat,
		&defenseInterval, &judgeConcurrency,
		&startTime, &endTime, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("query contest error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	ct.StartTime = startTime.Format("2006-01-02 15:04")
	ct.EndTime = endTime.Format("2006-01-02 15:04")
	ct.CreatedAt = createdAt.Format("2006-01-02 15:04")
	ct.UpdatedAt = updatedAt.Format("2006-01-02 15:04")
	if flagFormat.Valid {
		ct.FlagFormat = flagFormat.String
	} else {
		ct.FlagFormat = "flag{[GUID]}"
	}
	// AWD-F 配置
	if defenseInterval.Valid {
		ct.DefenseInterval = int(defenseInterval.Int64)
	} else {
		ct.DefenseInterval = 60
	}
	if judgeConcurrency.Valid {
		ct.JudgeConcurrency = int(judgeConcurrency.Int64)
	} else {
		ct.JudgeConcurrency = 5
	}

	c.JSON(http.StatusOK, ct)
}

// HandleUpdateContest 更新比赛
func HandleUpdateContest(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var req UpdateContestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查比赛是否存在
	var exists bool
	db.QueryRow(`SELECT EXISTS(SELECT 1 FROM contests WHERE id = $1)`, id).Scan(&exists)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	// 获取旧状态和模式（用于状态变更钩子）
	var oldStatus, oldMode string
	db.QueryRow(`SELECT status, mode FROM contests WHERE id = $1`, id).Scan(&oldStatus, &oldMode)

	// 动态构建更新语句
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if req.Name != "" {
		updates = append(updates, "name = $"+strconv.Itoa(argIndex))
		args = append(args, req.Name)
		argIndex++
	}
	if req.Description != "" {
		updates = append(updates, "description = $"+strconv.Itoa(argIndex))
		args = append(args, req.Description)
		argIndex++
	}
	if req.Mode != "" {
		if req.Mode != "jeopardy" && req.Mode != "awd" && req.Mode != "awd-f" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_MODE"})
			return
		}
		updates = append(updates, "mode = $"+strconv.Itoa(argIndex))
		args = append(args, req.Mode)
		argIndex++
	}
	if req.Status != "" {
		if req.Status != "pending" && req.Status != "running" && req.Status != "paused" && req.Status != "ended" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
			return
		}
		updates = append(updates, "status = $"+strconv.Itoa(argIndex))
		args = append(args, req.Status)
		argIndex++
	}
	if req.CoverImage != "" {
		updates = append(updates, "cover_image = $"+strconv.Itoa(argIndex))
		args = append(args, req.CoverImage)
		argIndex++
	}
	if req.StartTime != "" {
		startTime, err := time.ParseInLocation("2006-01-02T15:04", req.StartTime, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_START_TIME"})
			return
		}
		updates = append(updates, "start_time = $"+strconv.Itoa(argIndex))
		args = append(args, startTime)
		argIndex++
	}
	if req.EndTime != "" {
		endTime, err := time.ParseInLocation("2006-01-02T15:04", req.EndTime, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_END_TIME"})
			return
		}
		updates = append(updates, "end_time = $"+strconv.Itoa(argIndex))
		args = append(args, endTime)
		argIndex++
	}
	if req.TeamLimit != nil {
		updates = append(updates, "team_limit = $"+strconv.Itoa(argIndex))
		args = append(args, *req.TeamLimit)
		argIndex++
	}
	if req.ContainerLimit != nil {
		updates = append(updates, "container_limit = $"+strconv.Itoa(argIndex))
		args = append(args, *req.ContainerLimit)
		argIndex++
	}

	// AWD-F 配置
	if req.DefenseInterval != nil {
		updates = append(updates, "defense_interval = $"+strconv.Itoa(argIndex))
		args = append(args, *req.DefenseInterval)
		argIndex++
	}
	if req.JudgeConcurrency != nil {
		updates = append(updates, "judge_concurrency = $"+strconv.Itoa(argIndex))
		args = append(args, *req.JudgeConcurrency)
		argIndex++
	}

	// 处理FlagFormat：如果改变了格式，需要删除所有已生成的Flag
	var flagFormatChanged bool
	if req.FlagFormat != nil {
		// 获取旧的格式
		var oldFormat sql.NullString
		db.QueryRow(`SELECT flag_format FROM contests WHERE id = $1`, id).Scan(&oldFormat)
		oldFmt := "flag{[GUID]}"
		if oldFormat.Valid && oldFormat.String != "" {
			oldFmt = oldFormat.String
		}
		
		newFmt := *req.FlagFormat
		if newFmt == "" {
			newFmt = "flag{[GUID]}"
		}
		
		if newFmt != oldFmt {
			flagFormatChanged = true
			updates = append(updates, "flag_format = $"+strconv.Itoa(argIndex))
			args = append(args, newFmt)
			argIndex++
		}
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, id)

	query := "UPDATE contests SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIndex)
	_, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("update contest error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 如果Flag格式改变，删除该比赛所有已生成的Flag，重启容器时会重新生成
	if flagFormatChanged {
		result, _ := db.Exec(`DELETE FROM team_challenge_flags WHERE contest_id = $1`, id)
		affected, _ := result.RowsAffected()
		log.Printf("[FlagFormat] Contest %s flag format changed, deleted %d flags", id, affected)
	}

	// AWD-F 比赛状态变更钩子：启动/销毁容器
	newStatus := req.Status
	if newStatus == "" {
		newStatus = oldStatus
	}
	newMode := req.Mode
	if newMode == "" {
		newMode = oldMode
	}
	if OnAWDFContestStatusChange != nil && oldStatus != newStatus {
		contestIDInt, _ := strconv.ParseInt(id, 10, 64)
		go OnAWDFContestStatusChange(db, contestIDInt, oldStatus, newStatus, newMode)
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED", "flagsRegenerated": flagFormatChanged})
}

// HandleDeleteContest 删除比赛
func HandleDeleteContest(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 先清理没有 CASCADE 的关联表
	// system_logs 表的 contest_id 没有 ON DELETE CASCADE
	db.Exec(`UPDATE system_logs SET contest_id = NULL WHERE contest_id = $1`, id)

	result, err := db.Exec(`DELETE FROM contests WHERE id = $1`, id)
	if err != nil {
		log.Printf("delete contest error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "DELETED"})
}

// HandlePublicContests 公开的比赛列表
func HandlePublicContests(c *gin.Context, db *sql.DB) {
	// 自动更新比赛状态（包括触发 AWD-F 钩子）
	autoUpdateContestStatus(db)

	status := c.Query("status")
	
	var userOrgID int64 = 0
	var isAdmin bool = false
		
	// 尝试解析 Authorization 头中的 token
	authHeader := c.GetHeader("Authorization")
	log.Printf("[DEBUG] authHeader: %s", authHeader)
	if authHeader != "" {
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString != "" {
			parts := strings.Split(tokenString, ".")
			if len(parts) == 3 {
				payload, err := base64.RawURLEncoding.DecodeString(parts[1])
				if err == nil {
					var claims map[string]interface{}
					if json.Unmarshal(payload, &claims) == nil {
						log.Printf("[DEBUG] JWT claims: %+v", claims)
						if uid, ok := claims["sub"].(float64); ok {
							var orgID int64
							var role string
							err := db.QueryRow(`SELECT COALESCE(organization_id, 0), role FROM users WHERE id = $1`, int64(uid)).Scan(&orgID, &role)
							if err == nil {
								userOrgID = orgID
								isAdmin = (role == "super")
								log.Printf("[DEBUG] User %d, OrgID: %d, Role: %s, IsAdmin: %v", int64(uid), userOrgID, role, isAdmin)
							} else {
								log.Printf("[DEBUG] Query user error: %v", err)
							}
						}
					}
				} else {
					log.Printf("[DEBUG] base64 decode error: %v", err)
				}
			}
		}
	}

	var rows *sql.Rows
	var err error

	baseQuery := `
		SELECT DISTINCT c.id, c.name, COALESCE(c.description,''), c.mode, c.status, c.cover_image,
		       c.start_time, c.end_time,
		       CASE c.status WHEN 'running' THEN 1 WHEN 'paused' THEN 2 WHEN 'pending' THEN 3 ELSE 4 END as status_order
		FROM contests c
		LEFT JOIN contest_organizations co ON c.id = co.contest_id
		WHERE 1=1`
	
	orderBy := `
		ORDER BY status_order, c.start_time DESC`

	if isAdmin {
		if status != "" {
			rows, err = db.Query(baseQuery+" AND c.status = $1"+orderBy, status)
		} else {
			rows, err = db.Query(baseQuery + orderBy)
		}
	} else if userOrgID > 0 {
		orgFilter := ` AND (
			NOT EXISTS (SELECT 1 FROM contest_organizations WHERE contest_id = c.id)
			OR co.organization_id = $1
		)`
		if status != "" {
			rows, err = db.Query(baseQuery+orgFilter+" AND c.status = $2"+orderBy, userOrgID, status)
		} else {
			rows, err = db.Query(baseQuery+orgFilter+orderBy, userOrgID)
		}
	} else {
		orgFilter := ` AND NOT EXISTS (SELECT 1 FROM contest_organizations WHERE contest_id = c.id)`
		if status != "" {
			rows, err = db.Query(baseQuery+orgFilter+" AND c.status = $1"+orderBy, status)
		} else {
			rows, err = db.Query(baseQuery + orgFilter + orderBy)
		}
	}

	if err != nil {
		log.Printf("query public contests error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var contests []Contest
	for rows.Next() {
		var ct Contest
		var startTime, endTime time.Time
		var statusOrder int
		if err := rows.Scan(&ct.ID, &ct.Name, &ct.Description, &ct.Mode, &ct.Status, &ct.CoverImage,
			&startTime, &endTime, &statusOrder); err != nil {
			log.Printf("scan public contest error: %v", err)
			continue
		}
		ct.StartTime = startTime.Format("2006-01-02 15:04")
		ct.EndTime = endTime.Format("2006-01-02 15:04")
		contests = append(contests, ct)
	}

	if contests == nil {
		contests = []Contest{}
	}

	c.JSON(http.StatusOK, gin.H{"contests": contests})
}

// HandleGetBonusConfig 获取三血奖励配置
func HandleGetBonusConfig(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var firstBlood, secondBlood, thirdBlood int
	err := db.QueryRow(`
		SELECT COALESCE(first_blood_bonus, 5), COALESCE(second_blood_bonus, 3), COALESCE(third_blood_bonus, 1)
		FROM contests WHERE id = $1`, id).Scan(&firstBlood, &secondBlood, &thirdBlood)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("query bonus config error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"firstBlood":  firstBlood,
		"secondBlood": secondBlood,
		"thirdBlood":  thirdBlood,
	})
}

// HandleUpdateBonusConfig 更新三血奖励配置
func HandleUpdateBonusConfig(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var req struct {
		FirstBlood  int `json:"firstBlood"`
		SecondBlood int `json:"secondBlood"`
		ThirdBlood  int `json:"thirdBlood"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 验证范围
	if req.FirstBlood < 0 || req.FirstBlood > 100 ||
		req.SecondBlood < 0 || req.SecondBlood > 100 ||
		req.ThirdBlood < 0 || req.ThirdBlood > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_BONUS_VALUE"})
		return
	}

	result, err := db.Exec(`
		UPDATE contests SET 
			first_blood_bonus = $1, 
			second_blood_bonus = $2, 
			third_blood_bonus = $3,
			updated_at = NOW()
		WHERE id = $4`, req.FirstBlood, req.SecondBlood, req.ThirdBlood, id)

	if err != nil {
		log.Printf("update bonus config error: %v", err)
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
