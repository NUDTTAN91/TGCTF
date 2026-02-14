// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package docker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"tgctf/server/logs"
)

// AdminInstanceInfo 管理端实例信息
type AdminInstanceInfo struct {
	ID            int64             `json:"id"`
	ContainerID   string            `json:"containerId"`
	ContainerName string            `json:"containerName"`
	TeamID        int64             `json:"teamId"`
	TeamName      string            `json:"teamName"`
	ContestID     int64             `json:"contestId"`
	ContestName   string            `json:"contestName"`
	ChallengeID   int64             `json:"challengeId"`
	ChallengeName string            `json:"challengeName"`
	UserID        int64             `json:"userId"`
	UserName      string            `json:"userName"`
	Ports         map[string]string `json:"ports"`
	Status        string            `json:"status"`
	ExpiresAt     string            `json:"expiresAt"`
	CreatedAt     string            `json:"createdAt"`
	IsExpired     bool              `json:"isExpired"`
}

// HandleAdminListInstances 获取所有容器实例列表
func HandleAdminListInstances(c *gin.Context, db *sql.DB) {
	status := c.Query("status") // running | all
	search := c.Query("search")
	contestID := c.Query("contestId")

	// 使用 UNION ALL 合并两个表的查询
	baseQuery := `
		SELECT * FROM (
			SELECT 
				ti.id, ti.container_id, ti.container_name,
				ti.team_id, COALESCE(t.name, '无队伍') as team_name,
				ti.contest_id, COALESCE(ct.name, '未知比赛') as contest_name,
				ti.challenge_id, COALESCE(q.title, '未知题目') as challenge_name,
				COALESCE(ti.created_by, 0), COALESCE(u.display_name, '-') as user_name,
				ti.ports, ti.status, ti.expires_at, ti.created_at,
				'jeopardy' as source_table
			FROM team_instances ti
			LEFT JOIN teams t ON ti.team_id = t.id
			LEFT JOIN contests ct ON ti.contest_id = ct.id
			LEFT JOIN contest_challenges cc ON ti.challenge_id = cc.id
			LEFT JOIN question_bank q ON cc.question_id = q.id
			LEFT JOIN users u ON ti.created_by = u.id
			UNION ALL
			SELECT 
				tia.id, tia.container_id, tia.container_name,
				tia.team_id, COALESCE(t.name, '无队伍') as team_name,
				tia.contest_id, COALESCE(ct.name, '未知比赛') as contest_name,
				tia.challenge_id, COALESCE(qa.title, '未知题目') as challenge_name,
				COALESCE(tia.created_by, 0), COALESCE(u.display_name, '系统') as user_name,
				tia.ports, tia.status, tia.expires_at, tia.created_at,
				'awdf' as source_table
			FROM team_instances_awdf tia
			LEFT JOIN teams t ON tia.team_id = t.id
			LEFT JOIN contests ct ON tia.contest_id = ct.id
			LEFT JOIN contest_challenges_awdf cca ON tia.challenge_id = cca.id
			LEFT JOIN question_bank_awdf qa ON cca.question_id = qa.id
			LEFT JOIN users u ON tia.created_by = u.id
		) combined WHERE 1=1
	`
	args := []interface{}{}
	argIdx := 1

	// 状态过滤
	if status == "" || status == "running" {
		baseQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, "running")
		argIdx++
	}

	// 比赛过滤
	if contestID != "" {
		baseQuery += fmt.Sprintf(" AND contest_id = $%d", argIdx)
		args = append(args, contestID)
		argIdx++
	}

	// 搜索过滤
	if search != "" {
		baseQuery += fmt.Sprintf(" AND (container_id ILIKE $%d OR container_name ILIKE $%d OR team_name ILIKE $%d OR user_name ILIKE $%d OR challenge_name ILIKE $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3, argIdx+4)
		searchPattern := "%" + search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern)
		argIdx += 5
	}

	baseQuery += " ORDER BY created_at DESC"

	rows, err := db.Query(baseQuery, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败", "details": err.Error()})
		return
	}
	defer rows.Close()

	var instances []AdminInstanceInfo
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)

	for rows.Next() {
		var inst AdminInstanceInfo
		var portsJSON string
		var expiresAt, createdAt time.Time
		var sourceTable string
		err := rows.Scan(&inst.ID, &inst.ContainerID, &inst.ContainerName,
			&inst.TeamID, &inst.TeamName,
			&inst.ContestID, &inst.ContestName,
			&inst.ChallengeID, &inst.ChallengeName,
			&inst.UserID, &inst.UserName,
			&portsJSON, &inst.Status, &expiresAt, &createdAt, &sourceTable)
		if err != nil {
			continue
		}
		json.Unmarshal([]byte(portsJSON), &inst.Ports)
		inst.ExpiresAt = expiresAt.Format("2006-01-02 15:04:05")
		inst.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		inst.IsExpired = now.After(expiresAt)
		instances = append(instances, inst)
	}

	c.JSON(http.StatusOK, gin.H{
		"instances": instances,
		"total":     len(instances),
	})
}

// HandleAdminDestroyInstance 管理员强制销毁容器
func HandleAdminDestroyInstance(c *gin.Context, db *sql.DB) {
	instanceID := c.Param("instanceId")

	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	adminID := int64(claimsMap["sub"].(float64))

	// 获取实例信息
	var containerID, containerName, challengeName, teamName string
	var contestID, challengeID, teamID int64
	err := db.QueryRow(`
		SELECT ti.container_id, ti.container_name, ti.contest_id, ti.challenge_id, ti.team_id,
			CASE WHEN ct.mode = 'awd-f' 
				THEN COALESCE(qa.title, '未知题目') 
				ELSE COALESCE(q.title, '未知题目') 
			END, 
			COALESCE(t.name, '未知队伍')
		FROM team_instances ti
		LEFT JOIN contests ct ON ti.contest_id = ct.id
		LEFT JOIN contest_challenges cc ON ti.challenge_id = cc.id AND ct.mode != 'awd-f'
		LEFT JOIN question_bank q ON cc.question_id = q.id
		LEFT JOIN contest_challenges_awdf cca ON ti.challenge_id = cca.id AND ct.mode = 'awd-f'
		LEFT JOIN question_bank_awdf qa ON cca.question_id = qa.id
		LEFT JOIN teams t ON ti.team_id = t.id
		WHERE ti.id = $1`, instanceID).Scan(&containerID, &containerName, &contestID, &challengeID, &teamID, &challengeName, &teamName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "实例不存在"})
		return
	}

	// 停止并删除容器
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()

	// 更新数据库状态
	_, err = db.Exec(`UPDATE team_instances SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, instanceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新状态失败"})
		return
	}

	// 记录日志
	clientIP := c.ClientIP()
	var adminName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, adminID).Scan(&adminName)
	logs.WriteLog(db, logs.TypeContainerDestroy, logs.LevelWarning, &adminID, &teamID, &contestID, &challengeID, clientIP,
		fmt.Sprintf("管理员 %s 强制销毁容器: 队伍[%s] 题目[%s]", adminName, teamName, challengeName),
		map[string]interface{}{"containerId": containerID, "containerName": containerName})

	c.JSON(http.StatusOK, gin.H{"message": "容器已销毁"})
}

// HandleAdminCleanExpired 批量清理过期容器
func HandleAdminCleanExpired(c *gin.Context, db *sql.DB) {
	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	adminID := int64(claimsMap["sub"].(float64))

	// 查询所有过期的运行中实例
	rows, err := db.Query(`
		SELECT id, container_id, container_name, team_id, contest_id, challenge_id
		FROM team_instances
		WHERE status = 'running' AND expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()

	var cleaned int
	var failed int
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for rows.Next() {
		var id int64
		var containerID, containerName string
		var teamID, contestID, challengeID int64
		rows.Scan(&id, &containerID, &containerName, &teamID, &contestID, &challengeID)

		// 停止容器
		err := exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
		if err != nil {
			failed++
			continue
		}

		// 更新状态
		db.Exec(`UPDATE team_instances SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
		cleaned++
	}

	// 记录日志
	clientIP := c.ClientIP()
	var adminName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, adminID).Scan(&adminName)
	logs.WriteLog(db, logs.TypeContainerDestroy, logs.LevelInfo, &adminID, nil, nil, nil, clientIP,
		fmt.Sprintf("管理员 %s 批量清理过期容器: 成功%d个, 失败%d个", adminName, cleaned, failed),
		map[string]interface{}{"cleaned": cleaned, "failed": failed})

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("清理完成：成功%d个，失败%d个", cleaned, failed),
		"cleaned": cleaned,
		"failed":  failed,
	})
}

// HandleAdminGetStats 获取Docker实例统计信息
func HandleAdminGetStats(c *gin.Context, db *sql.DB) {
	// 运行中的容器数量
	var runningCount int
	db.QueryRow(`SELECT COUNT(*) FROM team_instances WHERE status = 'running'`).Scan(&runningCount)

	// 过期待清理的容器数量
	var expiredCount int
	db.QueryRow(`SELECT COUNT(*) FROM team_instances WHERE status = 'running' AND expires_at < CURRENT_TIMESTAMP`).Scan(&expiredCount)

	// 今日创建的容器数量
	var todayCreated int
	db.QueryRow(`SELECT COUNT(*) FROM team_instances WHERE created_at >= CURRENT_DATE`).Scan(&todayCreated)

	// 今日销毁的容器数量
	var todayDestroyed int
	db.QueryRow(`SELECT COUNT(*) FROM team_instances WHERE status = 'destroyed' AND updated_at >= CURRENT_DATE`).Scan(&todayDestroyed)

	// 获取真实 Docker 容器数量（tg 前缀的）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-q", "--filter", "name=tg_")
	output, _ := cmd.Output()
	dockerCount := 0
	if len(output) > 0 {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, line := range lines {
			if line != "" {
				dockerCount++
			}
		}
	}

	// 按比赛统计
	type ContestStat struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Count int    `json:"count"`
	}
	var contestStats []ContestStat
	contestRows, err := db.Query(`
		SELECT c.id, c.name, COUNT(*) as count
		FROM team_instances ti
		JOIN contests c ON ti.contest_id = c.id
		WHERE ti.status = 'running'
		GROUP BY c.id, c.name
		ORDER BY count DESC
		LIMIT 10`)
	if err == nil {
		defer contestRows.Close()
		for contestRows.Next() {
			var stat ContestStat
			contestRows.Scan(&stat.ID, &stat.Title, &stat.Count)
			contestStats = append(contestStats, stat)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"runningCount":   runningCount,
		"expiredCount":   expiredCount,
		"todayCreated":   todayCreated,
		"todayDestroyed": todayDestroyed,
		"dockerCount":    dockerCount,
		"contestStats":   contestStats,
	})
}

// HandleAdminBatchDestroy 批量销毁选中的容器
func HandleAdminBatchDestroy(c *gin.Context, db *sql.DB) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择要销毁的实例"})
		return
	}

	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	adminID := int64(claimsMap["sub"].(float64))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var success, failed int
	for _, id := range req.IDs {
		var containerID string
		err := db.QueryRow(`SELECT container_id FROM team_instances WHERE id = $1 AND status = 'running'`, id).Scan(&containerID)
		if err != nil {
			failed++
			continue
		}

		err = exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
		if err != nil {
			failed++
			continue
		}

		db.Exec(`UPDATE team_instances SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
		success++
	}

	// 记录日志
	clientIP := c.ClientIP()
	var adminName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, adminID).Scan(&adminName)
	logs.WriteLog(db, logs.TypeContainerDestroy, logs.LevelWarning, &adminID, nil, nil, nil, clientIP,
		fmt.Sprintf("管理员 %s 批量销毁容器: 成功%d个, 失败%d个", adminName, success, failed),
		map[string]interface{}{"ids": req.IDs, "success": success, "failed": failed})

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("批量销毁完成：成功%d个，失败%d个", success, failed),
		"success": success,
		"failed":  failed,
	})
}

// HandleAdminGetContainerLogs 获取容器日志
func HandleAdminGetContainerLogs(c *gin.Context, db *sql.DB) {
	instanceID := c.Param("instanceId")
	lines := c.DefaultQuery("lines", "100")

	var containerID string
	err := db.QueryRow(`SELECT container_id FROM team_instances WHERE id = $1`, instanceID).Scan(&containerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "实例不存在"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	linesInt, _ := strconv.Atoi(lines)
	if linesInt <= 0 || linesInt > 500 {
		linesInt = 100
	}

	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(linesInt), containerID)
	output, err := cmd.CombinedOutput()

	c.JSON(http.StatusOK, gin.H{
		"logs":        string(output),
		"containerId": containerID,
	})
}

// HandleAdminGetContests 获取比赛列表（用于下拉选择）
func HandleAdminGetContestsForDocker(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT c.id, c.name, COUNT(ti.id) as instance_count
		FROM contests c
		LEFT JOIN team_instances ti ON c.id = ti.contest_id AND ti.status = 'running'
		GROUP BY c.id, c.name
		ORDER BY c.created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	defer rows.Close()

	type ContestOption struct {
		ID            int64  `json:"id"`
		Title         string `json:"title"`
		InstanceCount int    `json:"instanceCount"`
	}
	var contests []ContestOption
	for rows.Next() {
		var opt ContestOption
		rows.Scan(&opt.ID, &opt.Title, &opt.InstanceCount)
		contests = append(contests, opt)
	}

	c.JSON(http.StatusOK, gin.H{"contests": contests})
}
