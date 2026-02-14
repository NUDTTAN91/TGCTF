// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ExpResult EXP执行结果
type ExpResult struct {
	ID             int64  `json:"id"`
	ContestID      int64  `json:"contestId"`
	ChallengeID    int64  `json:"challengeId"`
	TeamID         int64  `json:"teamId"`
	TeamName       string `json:"teamName"`
	RoundNumber    int    `json:"roundNumber"`
	ExpSuccess     bool   `json:"expSuccess"`
	CheckSuccess   bool   `json:"checkSuccess"`
	DefenseSuccess bool   `json:"defenseSuccess"`
	ScoreEarned    int    `json:"scoreEarned"`
	ExpOutput      string `json:"expOutput"`
	CheckOutput    string `json:"checkOutput"`
	ExecutedAt     string `json:"executedAt"`
}

// RunEXPAttack 对单个队伍执行EXP攻击
func RunEXPAttack(db *sql.DB, contestID, challengeID int64, teamID int64, roundNumber int) (*ExpResult, error) {
	// 获取题目的EXP脚本和检测脚本
	var expScript, checkScript sql.NullString
	var defenseScore int
	err := db.QueryRow(`
		SELECT q.exp_script, q.check_script, cc.defense_score
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.id = $1
	`, challengeID).Scan(&expScript, &checkScript, &defenseScore)
	if err != nil {
		return nil, fmt.Errorf("获取题目配置失败: %v", err)
	}

	// 获取队伍容器的端口映射
	var ports string
	var containerID string
	err = db.QueryRow(`
		SELECT container_id, ports FROM team_instances_awdf 
		WHERE team_id = $1 AND contest_id = $2 AND challenge_id = $3 AND status = 'running'
	`, teamID, contestID, challengeID).Scan(&containerID, &ports)
	if err != nil {
		return nil, fmt.Errorf("未找到运行中的容器: %v", err)
	}

	// 使用 Docker bridge 网关 IP + 主机映射端口（从容器内访问宿主机端口）
	// 172.17.0.1 是 Docker 默认 bridge 网络的网关，可以用来访问宿主机
	targetIP := "172.17.0.1"
	targetPort := "80"

	// 解析端口映射，取 80 端口对应的主机端口
	// 格式: {"22":"65413","80":"65412"}
	if ports != "" && strings.HasPrefix(ports, "{") {
		var portMap map[string]string
		if err := json.Unmarshal([]byte(ports), &portMap); err == nil {
			// 优先取 80 端口
			if hostPort, ok := portMap["80"]; ok {
				targetPort = hostPort
			} else {
				// 如果没有 80，取第一个非 22 的端口
				for containerPort, hostPort := range portMap {
					if containerPort != "22" {
						targetPort = hostPort
						break
					}
				}
			}
		}
	}

	result := &ExpResult{
		ContestID:   contestID,
		ChallengeID: challengeID,
		TeamID:      teamID,
		RoundNumber: roundNumber,
	}

	// 执行EXP脚本
	if expScript.Valid && expScript.String != "" {
		expSuccess, expOutput := executeScript(expScript.String, targetIP, targetPort, 30)
		result.ExpSuccess = expSuccess
		result.ExpOutput = expOutput
	} else {
		result.ExpOutput = "未配置EXP脚本"
	}

	// 执行功能检测脚本
	if checkScript.Valid && checkScript.String != "" {
		checkSuccess, checkOutput := executeScript(checkScript.String, targetIP, targetPort, 15)
		result.CheckSuccess = checkSuccess
		result.CheckOutput = checkOutput
	} else {
		result.CheckSuccess = true
		result.CheckOutput = "未配置检测脚本，默认通过"
	}

	// 判断防守是否成功：EXP失败 且 功能检测通过
	result.DefenseSuccess = !result.ExpSuccess && result.CheckSuccess

	// 计算得分
	if result.DefenseSuccess {
		result.ScoreEarned = defenseScore
	}

	// 保存结果到数据库
	var resultID int64
	err = db.QueryRow(`
		INSERT INTO awdf_exp_results 
		(contest_id, challenge_id, team_id, round_number, exp_success, check_success, defense_success, score_earned, exp_output, check_output)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, contestID, challengeID, teamID, roundNumber,
		result.ExpSuccess, result.CheckSuccess, result.DefenseSuccess, result.ScoreEarned,
		truncateOutput(result.ExpOutput, 2000), truncateOutput(result.CheckOutput, 2000),
	).Scan(&resultID)

	if err != nil {
		return nil, fmt.Errorf("保存结果失败: %v", err)
	}
	result.ID = resultID
	result.ExecutedAt = time.Now().Format(time.RFC3339)

	return result, nil
}

// executeScript 执行脚本并返回结果
func executeScript(script, targetIP, targetPort string, timeoutSeconds int) (success bool, output string) {
	// 创建临时脚本文件
	tempFile, err := os.CreateTemp("", "exp-*.sh")
	if err != nil {
		return false, fmt.Sprintf("创建脚本文件失败: %v", err)
	}
	defer os.Remove(tempFile.Name())

	// 写入脚本内容
	tempFile.WriteString(script)
	tempFile.Close()
	os.Chmod(tempFile.Name(), 0755)

	// 执行脚本
	cmd := exec.Command("timeout", fmt.Sprintf("%d", timeoutSeconds), "sh", tempFile.Name(), targetIP, targetPort)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	combinedOutput := stdout.String() + stderr.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// 脚本返回非0退出码
			return false, fmt.Sprintf("退出码: %d\n%s", exitErr.ExitCode(), combinedOutput)
		}
		return false, fmt.Sprintf("执行失败: %v\n%s", err, combinedOutput)
	}

	// 退出码为0表示成功
	return true, combinedOutput
}

// getContainerIP 获取容器IP
func getContainerIP(containerID string) (string, error) {
	cmd := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerID)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(output))
	if ip == "" {
		return "", fmt.Errorf("容器没有IP地址")
	}
	return ip, nil
}

// truncateOutput 截断输出
func truncateOutput(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "...(truncated)"
	}
	return s
}

// HandleGetExpResults 获取EXP执行结果
func HandleGetExpResults(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Query("challengeId")
	teamID := c.Query("teamId")

	query := `
		SELECT r.id, r.contest_id, r.challenge_id, r.team_id, t.name,
			r.round_number, r.exp_success, r.check_success, r.defense_success, 
			r.score_earned, r.exp_output, r.check_output, r.executed_at
		FROM awdf_exp_results r
		JOIN teams t ON r.team_id = t.id
		WHERE r.contest_id = $1
	`
	args := []interface{}{contestID}
	argIndex := 2

	if challengeID != "" {
		query += fmt.Sprintf(" AND r.challenge_id = $%d", argIndex)
		args = append(args, challengeID)
		argIndex++
	}
	if teamID != "" {
		query += fmt.Sprintf(" AND r.team_id = $%d", argIndex)
		args = append(args, teamID)
		argIndex++
	}

	query += " ORDER BY r.executed_at DESC LIMIT 100"

	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	var results []ExpResult
	for rows.Next() {
		var r ExpResult
		var executedAt time.Time
		var expOutput, checkOutput sql.NullString

		err := rows.Scan(
			&r.ID, &r.ContestID, &r.ChallengeID, &r.TeamID, &r.TeamName,
			&r.RoundNumber, &r.ExpSuccess, &r.CheckSuccess, &r.DefenseSuccess,
			&r.ScoreEarned, &expOutput, &checkOutput, &executedAt,
		)
		if err != nil {
			continue
		}
		if expOutput.Valid {
			r.ExpOutput = expOutput.String
		}
		if checkOutput.Valid {
			r.CheckOutput = checkOutput.String
		}
		r.ExecutedAt = executedAt.Format(time.RFC3339)
		results = append(results, r)
	}

	if results == nil {
		results = []ExpResult{}
	}

	c.JSON(http.StatusOK, results)
}

// HandleManualRunExp 手动触发EXP攻击（管理员测试用）
func HandleManualRunExp(c *gin.Context, db *sql.DB) {
	var req struct {
		ChallengeID int64 `json:"challengeId" binding:"required"`
		TeamID      int64 `json:"teamId" binding:"required"`
		RoundNumber int   `json:"roundNumber"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	contestID := c.Param("id")

	// 获取当前轮次（如果没指定）
	if req.RoundNumber == 0 {
		db.QueryRow(`
			SELECT COALESCE(MAX(round_number), 0) + 1 FROM awdf_exp_results 
			WHERE contest_id = $1 AND challenge_id = $2 AND team_id = $3
		`, contestID, req.ChallengeID, req.TeamID).Scan(&req.RoundNumber)
	}

	// 解析contestID
	var cid int64
	fmt.Sscanf(contestID, "%d", &cid)

	result, err := RunEXPAttack(db, cid, req.ChallengeID, req.TeamID, req.RoundNumber)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "EXP_FAILED", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// HandleGetTeamDefenseStats 获取队伍防守统计
func HandleGetTeamDefenseStats(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Query("challengeId")

	query := `
		SELECT t.id, t.name, 
			COUNT(*) as total_rounds,
			SUM(CASE WHEN r.defense_success THEN 1 ELSE 0 END) as defense_count,
			SUM(r.score_earned) as total_score
		FROM awdf_exp_results r
		JOIN teams t ON r.team_id = t.id
		WHERE r.contest_id = $1
	`
	args := []interface{}{contestID}

	if challengeID != "" {
		query += " AND r.challenge_id = $2"
		args = append(args, challengeID)
	}

	query += " GROUP BY t.id, t.name ORDER BY total_score DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	type TeamStats struct {
		TeamID       int64  `json:"teamId"`
		TeamName     string `json:"teamName"`
		TotalRounds  int    `json:"totalRounds"`
		DefenseCount int    `json:"defenseCount"`
		TotalScore   int    `json:"totalScore"`
	}

	var stats []TeamStats
	for rows.Next() {
		var s TeamStats
		rows.Scan(&s.TeamID, &s.TeamName, &s.TotalRounds, &s.DefenseCount, &s.TotalScore)
		stats = append(stats, s)
	}

	if stats == nil {
		stats = []TeamStats{}
	}

	c.JSON(http.StatusOK, stats)
}
