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

// TeamInstance 队伍容器实例
type TeamInstance struct {
	ID            int64             `json:"id"`
	TeamID        int64             `json:"teamId"`
	ContestID     int64             `json:"contestId"`
	ChallengeID   int64             `json:"challengeId"`
	ContainerID   string            `json:"containerId"`
	ContainerName string            `json:"containerName"`
	Ports         map[string]string `json:"ports"`
	Status        string            `json:"status"`
	ExpiresAt     string            `json:"expiresAt"`
	TTL           int               `json:"ttl"`
	CreatedAt     string            `json:"createdAt"`
}

// 系统设置获取函数的类型定义
type SettingsGetter func(db *sql.DB) int

// 端口分配函数类型
type PortAllocator func(db *sql.DB, count int) ([]int, error)

// 全局变量，用于注入系统设置获取函数
var GetContainerTTL SettingsGetter
var GetContainerExtendTTL SettingsGetter
var GetContainerExtendWindow SettingsGetter
var AllocatePorts PortAllocator

// HandleCreateUserInstance 创建队伍容器实例
func HandleCreateUserInstance(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Param("challengeId")
	forceDestroy := c.Query("force") == "true"
	fmt.Printf("[DEBUG] CreateTeamInstance: contestID=%s, challengeID=%s, force=%v\n", contestID, challengeID, forceDestroy)

	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	userID := int64(claimsMap["sub"].(float64))

	var teamID sql.NullInt64
	err := db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if err != nil || !teamID.Valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_TEAM", "message": "您还未加入队伍，无法部署容器"})
		return
	}
	fmt.Printf("[DEBUG] userID=%d, teamID=%d\n", userID, teamID.Int64)

	// 检查该题目是否已有实例
	var existingID int64
	var existingCreatedBy sql.NullInt64
	err = db.QueryRow(`SELECT id, created_by FROM team_instances WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'`,
		teamID.Int64, challengeID).Scan(&existingID, &existingCreatedBy)
	if err == nil {
		// 该题目已有实例
		if existingCreatedBy.Valid && existingCreatedBy.Int64 == userID {
			// 是自己创建的，返回已存在
			c.JSON(http.StatusConflict, gin.H{"error": "INSTANCE_EXISTS", "message": "您已有该题目的运行中实例"})
		} else {
			// 是队友创建的，不能再创建
			var creatorName string
			if existingCreatedBy.Valid {
				db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, existingCreatedBy.Int64).Scan(&creatorName)
			}
			c.JSON(http.StatusConflict, gin.H{"error": "INSTANCE_BY_TEAMMATE", "message": "队友 [" + creatorName + "] 已启动该题目的容器，您无法重复启动"})
		}
		return
	}
	fmt.Printf("[DEBUG] No existing instance for this challenge, checking container limit...\n")

	// 获取比赛容器限制
	var containerLimit int
	err = db.QueryRow(`SELECT COALESCE(container_limit, 1) FROM contests WHERE id = $1`, contestID).Scan(&containerLimit)
	if err != nil {
		containerLimit = 1 // 默认1个
	}
	fmt.Printf("[DEBUG] containerLimit=%d\n", containerLimit)

	// 检查队伍当前运行的容器数量
	var runningCount int
	db.QueryRow(`SELECT COUNT(*) FROM team_instances WHERE team_id = $1 AND contest_id = $2 AND status = 'running'`,
		teamID.Int64, contestID).Scan(&runningCount)
	fmt.Printf("[DEBUG] runningCount=%d\n", runningCount)

	// 如果达到限制
	if containerLimit > 0 && runningCount >= containerLimit {
		// 检查是否有自己创建的容器可以销毁
		var ownInstanceID int64
		var ownContainerID, ownChallengeName string
		var ownChallengeID int64
		err = db.QueryRow(`
			SELECT ti.id, ti.container_id, ti.challenge_id, COALESCE(q.title, '')
			FROM team_instances ti
			LEFT JOIN contest_challenges cc ON ti.challenge_id = cc.id
			LEFT JOIN question_bank q ON cc.question_id = q.id
			WHERE ti.team_id = $1 AND ti.contest_id = $2 AND ti.status = 'running' AND ti.created_by = $3
			ORDER BY ti.created_at ASC LIMIT 1`,
			teamID.Int64, contestID, userID).Scan(&ownInstanceID, &ownContainerID, &ownChallengeID, &ownChallengeName)
		
		if err == nil {
			// 有自己创建的容器
			if forceDestroy {
				// 强制销毁旧容器
				fmt.Printf("[DEBUG] Force destroying old container: %s\n", ownContainerID)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				exec.CommandContext(ctx, "docker", "rm", "-f", ownContainerID).Run()
				cancel()
				db.Exec(`UPDATE team_instances SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, ownInstanceID)
				runningCount--
				// 继续创建新容器
			} else {
				// 返回需要确认销毁
				c.JSON(http.StatusConflict, gin.H{
					"error":         "NEED_DESTROY_OWN",
					"message":       fmt.Sprintf("您已有运行中的容器 [%s]，需要先销毁才能启动新容器", ownChallengeName),
					"oldChallengeId": ownChallengeID,
					"oldChallengeName": ownChallengeName,
				})
				return
			}
		} else {
			// 没有自己创建的容器，是队友创建的
			c.JSON(http.StatusConflict, gin.H{
				"error":   "LIMIT_REACHED",
				"message": fmt.Sprintf("队伍容器数量已达上限(%d个)，且均由队友启动，您无法启动新容器", containerLimit),
			})
			return
		}
	}
	fmt.Printf("[DEBUG] Container limit check passed, continue...\n")

	// 先查询比赛模式
	var contestMode string
	err = db.QueryRow(`SELECT mode FROM contests WHERE id = $1`, contestID).Scan(&contestMode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CONTEST_NOT_FOUND", "message": "比赛不存在"})
		return
	}

	// AWD-F 模式下禁止用户手动创建容器（容器由系统在比赛开始时统一创建）
	if contestMode == "awd-f" {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "AWDF_NO_MANUAL_DEPLOY",
			"message": "AWD-F 模式下容器由系统统一创建，您只能使用重置容器功能",
		})
		return
	}

	var dockerImage, ports, cpuLimit, memoryLimit, flagEnv, flagScript sql.NullString
	var questionID int64
	
	if contestMode == "awd-f" {
		// AWD-F 模式：查询 contest_challenges_awdf 和 question_bank_awdf
		err = db.QueryRow(`
			SELECT q.id, q.docker_image, q.ports, q.cpu_limit, q.memory_limit, q.flag_env, q.flag_script
			FROM contest_challenges_awdf cc
			JOIN question_bank_awdf q ON cc.question_id = q.id
			WHERE cc.id = $1 AND cc.contest_id = $2`,
			challengeID, contestID).Scan(&questionID, &dockerImage, &ports, &cpuLimit, &memoryLimit, &flagEnv, &flagScript)
	} else {
		// Jeopardy/AWD 模式：查询 contest_challenges 和 question_bank
		err = db.QueryRow(`
			SELECT q.id, q.docker_image, q.ports, q.cpu_limit, q.memory_limit, q.flag_env, q.flag_script
			FROM contest_challenges cc
			JOIN question_bank q ON cc.question_id = q.id
			WHERE cc.id = $1 AND cc.contest_id = $2`,
			challengeID, contestID).Scan(&questionID, &dockerImage, &ports, &cpuLimit, &memoryLimit, &flagEnv, &flagScript)
	}
	if err != nil {
		fmt.Printf("[DEBUG] Query question failed: %v\n", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "CHALLENGE_NOT_FOUND", "message": "题目不存在"})
		return
	}
	fmt.Printf("[DEBUG] questionID=%d, dockerImage=%s, ports=%s\n", questionID, dockerImage.String, ports.String)

	if !dockerImage.Valid || dockerImage.String == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_DOCKER_IMAGE", "message": "该题目没有配置容器镜像"})
		return
	}

	flag := GetOrCreateTeamFlag(db, teamID.Int64, contestID, challengeID)

	var portList []string
	if ports.Valid && ports.String != "" {
		json.Unmarshal([]byte(ports.String), &portList)
	}

	containerName := fmt.Sprintf("tg_team_%d_%s_%d", teamID.Int64, challengeID, time.Now().Unix())
	args := []string{"run", "-d", "--name", containerName}

	// 从端口池分配端口
	portInfo := make(map[string]string)
	if len(portList) > 0 {
		if AllocatePorts != nil {
			allocatedPorts, err := AllocatePorts(db, len(portList))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "PORT_ALLOCATION_FAILED",
					"message": "端口分配失败: " + err.Error(),
				})
				return
			}
			for i, containerPort := range portList {
				hostPort := allocatedPorts[i]
				args = append(args, "-p", fmt.Sprintf("%d:%s", hostPort, containerPort))
				portInfo[containerPort] = strconv.Itoa(hostPort)
			}
			fmt.Printf("[DEBUG] Allocated ports: %v\n", portInfo)
		} else {
			// 回退到 Docker 自动分配
			for _, port := range portList {
				args = append(args, "-p", fmt.Sprintf(":%s", port))
			}
		}
	}

	if cpuLimit.Valid && cpuLimit.String != "" {
		args = append(args, "--cpus", cpuLimit.String)
	}
	if memoryLimit.Valid && memoryLimit.String != "" {
		args = append(args, "-m", memoryLimit.String)
	}

	// 处理 Flag 注入方式：环境变量 和/或 命令行参数
	useCmdArg := false
	if flagEnv.Valid && flagEnv.String != "" {
		envNames := strings.Split(flagEnv.String, ",")
		for _, en := range envNames {
			en = strings.TrimSpace(en)
			if en == "CMDARG" || en == "$1" {
				useCmdArg = true
			} else if en != "" {
				args = append(args, "-e", fmt.Sprintf("%s=%s", en, flag))
			}
		}
	} else {
		args = append(args, "-e", fmt.Sprintf("FLAG=%s", flag))
	}

	args = append(args, "--label", "tg.type=team")
	args = append(args, "--label", fmt.Sprintf("tg.team_id=%d", teamID.Int64))
	args = append(args, "--label", fmt.Sprintf("tg.challenge_id=%s", challengeID))

	args = append(args, dockerImage.String)

	// 如果配置了 CMDARG，将 flag 作为命令行参数追加到镜像名之后
	if useCmdArg {
		args = append(args, flag)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("[DEBUG] Docker args: %v\n", args)
	runCmd := exec.CommandContext(ctx, "docker", args...)
	output, err := runCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("[DEBUG] Docker run failed: %v, output: %s\n", err, string(output))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "CONTAINER_CREATE_FAILED",
			"message": "创建容器失败",
			"details": string(output),
		})
		return
	}

	outputLines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containerID := strings.TrimSpace(outputLines[len(outputLines)-1])
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	fmt.Printf("[DEBUG] Container ID: %s\n", containerID)

	// 如果使用 Docker 自动分配端口，需要查询端口映射
	if len(portInfo) == 0 && len(portList) > 0 {
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			portCmd := exec.CommandContext(ctx, "docker", "port", containerID)
			portOutput, err := portCmd.CombinedOutput()
			if err == nil {
				lines := strings.Split(string(portOutput), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" || strings.Contains(line, "[::") {
						continue
					}
					parts := strings.Split(line, " -> ")
					if len(parts) == 2 {
						containerPort := strings.Split(parts[0], "/")[0]
						hostAddr := parts[1]
						if idx := strings.LastIndex(hostAddr, ":"); idx != -1 {
							portInfo[containerPort] = hostAddr[idx+1:]
						}
					}
				}
				if len(portInfo) > 0 {
					break
				}
			}
			if i >= 2 {
				checkCmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", containerID)
				if out, err := checkCmd.Output(); err == nil && strings.TrimSpace(string(out)) != "true" {
					break
				}
			}
		}
	}

	// 如果配置了 flag_script，在容器启动后执行脚本注入 Flag
	if flagScript.Valid && flagScript.String != "" {
		fmt.Printf("[DEBUG] Executing flag script: %s\n", flagScript.String)
		time.Sleep(500 * time.Millisecond) // 等待容器完全启动
		scriptCmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", flagScript.String, flag)
		scriptOutput, scriptErr := scriptCmd.CombinedOutput()
		if scriptErr != nil {
			fmt.Printf("[DEBUG] Flag script execution failed: %v, output: %s\n", scriptErr, string(scriptOutput))
			// 脚本执行失败不阻塞容器创建，仅记录日志
		} else {
			fmt.Printf("[DEBUG] Flag script executed successfully\n")
		}
	}

	portsJSON, _ := json.Marshal(portInfo)
	initialTTL := 120 // 默认值
	if GetContainerTTL != nil {
		initialTTL = GetContainerTTL(db)
	}
	expiresAt := time.Now().Add(time.Duration(initialTTL) * time.Minute)

	var instanceID int64
	err = db.QueryRow(`
		INSERT INTO team_instances (team_id, contest_id, challenge_id, container_id, container_name, ports, status, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, 'running', $7, $8)
		ON CONFLICT (team_id, challenge_id) DO UPDATE SET
			container_id = $4, container_name = $5, ports = $6, status = 'running', expires_at = $7, created_by = $8, updated_at = CURRENT_TIMESTAMP
		RETURNING id`,
		teamID.Int64, contestID, challengeID, containerID, containerName, string(portsJSON), expiresAt, userID).Scan(&instanceID)
	if err != nil {
		fmt.Printf("[DEBUG] DB insert failed: %v\n", err)
		exec.Command("docker", "rm", "-f", containerID).Run()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR", "message": "保存实例失败", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"instanceId":    instanceID,
		"containerId":   containerID,
		"containerName": containerName,
		"ports":         portInfo,
		"expiresAt":     expiresAt.Format("2006-01-02 15:04:05"),
		"ttl":           initialTTL * 60,
		"message":       "容器创建成功",
	})

	// 记录容器创建日志
	clientIP := c.ClientIP()
	contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
	challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
	var displayName, challengeName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName)
	if contestMode == "awd-f" {
		db.QueryRow(`SELECT q.title FROM question_bank_awdf q JOIN contest_challenges_awdf cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
	} else {
		db.QueryRow(`SELECT q.title FROM question_bank q JOIN contest_challenges cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
	}
	logs.WriteLog(db, logs.TypeContainerCreate, logs.LevelSuccess, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
		displayName+" 启动题目 ["+challengeName+"] 的容器实例", map[string]interface{}{
			"containerId": containerID, "ports": portInfo,
		})
}

// HandleGetUserInstance 获取队伍容器实例
func HandleGetUserInstance(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("challengeId")

	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	userID := int64(claimsMap["sub"].(float64))

	var teamID sql.NullInt64
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_TEAM", "message": "您还未加入队伍"})
		return
	}

	var inst TeamInstance
	var portsJSON string
	var expiresAt time.Time
	var createdBy sql.NullInt64
	var found bool
	var isAWDF bool
	var sshPassword sql.NullString

	// 先查询普通容器实例表
	err := db.QueryRow(`
		SELECT id, team_id, contest_id, challenge_id, container_id, container_name, ports, status, expires_at, created_at, created_by
		FROM team_instances WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'`,
		teamID.Int64, challengeID).Scan(&inst.ID, &inst.TeamID, &inst.ContestID, &inst.ChallengeID,
		&inst.ContainerID, &inst.ContainerName, &portsJSON, &inst.Status, &expiresAt, &inst.CreatedAt, &createdBy)
	if err == nil {
		found = true
	} else {
		// 再查询 AWD-F 容器实例表（包含 ssh_password）
		err = db.QueryRow(`
			SELECT id, team_id, contest_id, challenge_id, container_id, container_name, ports, ssh_password, status, expires_at, created_at, created_by
			FROM team_instances_awdf WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'`,
			teamID.Int64, challengeID).Scan(&inst.ID, &inst.TeamID, &inst.ContestID, &inst.ChallengeID,
			&inst.ContainerID, &inst.ContainerName, &portsJSON, &sshPassword, &inst.Status, &expiresAt, &inst.CreatedAt, &createdBy)
		if err == nil {
			found = true
			isAWDF = true
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_INSTANCE", "message": "队伍没有运行中的实例"})
		return
	}

	json.Unmarshal([]byte(portsJSON), &inst.Ports)
	inst.ExpiresAt = expiresAt.Format("2006-01-02 15:04:05")

	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	expiresLocal := expiresAt
	if expiresAt.Location() == time.UTC {
		expiresLocal = time.Date(expiresAt.Year(), expiresAt.Month(), expiresAt.Day(),
			expiresAt.Hour(), expiresAt.Minute(), expiresAt.Second(), expiresAt.Nanosecond(), loc)
	}
	inst.TTL = int(expiresLocal.Sub(now).Seconds())
	if inst.TTL < 0 {
		inst.TTL = 0
	}

	// 返回是否是当前用户创建的
	isOwner := createdBy.Valid && createdBy.Int64 == userID
	var creatorName string
	if createdBy.Valid && createdBy.Int64 != userID {
		db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, createdBy.Int64).Scan(&creatorName)
	}

	// 构建响应
	response := gin.H{
		"id":            inst.ID,
		"teamId":        inst.TeamID,
		"contestId":     inst.ContestID,
		"challengeId":   inst.ChallengeID,
		"containerId":   inst.ContainerID,
		"containerName": inst.ContainerName,
		"ports":         inst.Ports,
		"status":        inst.Status,
		"expiresAt":     inst.ExpiresAt,
		"ttl":           inst.TTL,
		"createdAt":     inst.CreatedAt,
		"isOwner":       isOwner,
		"creatorName":   creatorName,
	}

	// AWD-F 模式：检查是否攻击成功（已解题），成功后才返回 SSH 信息
	if isAWDF && sshPassword.Valid {
		var hasSolved bool
		db.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM team_solves_awdf 
				WHERE team_id = $1 AND challenge_id = $2 AND contest_id = $3
			)
		`, teamID.Int64, challengeID, inst.ContestID).Scan(&hasSolved)
		
		if hasSolved {
			response["sshPassword"] = sshPassword.String
			response["sshUser"] = "root"  // 默认用户
			response["sshPort"] = "22"    // 容器内 SSH 端口
		}
	}

	c.JSON(http.StatusOK, response)
}

// HandleDestroyUserInstance 销毁队伍容器实例
func HandleDestroyUserInstance(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("challengeId")

	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	userID := int64(claimsMap["sub"].(float64))

	var teamID sql.NullInt64
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_TEAM", "message": "您还未加入队伍"})
		return
	}

	var containerID string
	var createdBy sql.NullInt64
	err := db.QueryRow(`SELECT container_id, created_by FROM team_instances WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'`,
		teamID.Int64, challengeID).Scan(&containerID, &createdBy)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_INSTANCE", "message": "队伍没有运行中的实例"})
		return
	}

	// 检查是否是创建者
	if createdBy.Valid && createdBy.Int64 != userID {
		var creatorName string
		db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, createdBy.Int64).Scan(&creatorName)
		c.JSON(http.StatusForbidden, gin.H{"error": "NOT_OWNER", "message": "该容器由队友 [" + creatorName + "] 创建，您无权销毁"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()

	db.Exec(`UPDATE team_instances SET status = 'destroyed', updated_at = CURRENT_TIMESTAMP WHERE team_id = $1 AND challenge_id = $2`,
		teamID.Int64, challengeID)

	// 记录容器销毁日志
	clientIP := c.ClientIP()
	contestID := c.Param("id")
	contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
	challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
	var displayName, challengeName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName)
	db.QueryRow(`SELECT q.title FROM question_bank q JOIN contest_challenges cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
	logs.WriteLog(db, logs.TypeContainerDestroy, logs.LevelSuccess, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
		displayName+" 销毁题目 ["+challengeName+"] 的容器实例", map[string]interface{}{
			"containerId": containerID,
		})

	c.JSON(http.StatusOK, gin.H{"message": "容器已销毁"})
}

// HandleExtendUserInstance 延长队伍容器实例时间
func HandleExtendUserInstance(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("challengeId")

	claims, _ := c.Get("claims")
	claimsMap := claims.(jwt.MapClaims)
	userID := int64(claimsMap["sub"].(float64))

	var teamID sql.NullInt64
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_TEAM", "message": "您还未加入队伍"})
		return
	}

	extendTTL := 120
	extendWindow := 15
	if GetContainerExtendTTL != nil {
		extendTTL = GetContainerExtendTTL(db)
	}
	if GetContainerExtendWindow != nil {
		extendWindow = GetContainerExtendWindow(db)
	}

	var currentExpiresAt time.Time
	err := db.QueryRow(`SELECT expires_at FROM team_instances WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'`,
		teamID.Int64, challengeID).Scan(&currentExpiresAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_INSTANCE", "message": "队伍没有运行中的实例"})
		return
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	expiresLocal := currentExpiresAt
	if currentExpiresAt.Location() == time.UTC {
		expiresLocal = time.Date(currentExpiresAt.Year(), currentExpiresAt.Month(), currentExpiresAt.Day(),
			currentExpiresAt.Hour(), currentExpiresAt.Minute(), currentExpiresAt.Second(), currentExpiresAt.Nanosecond(), loc)
	}
	remainingMinutes := int(expiresLocal.Sub(now).Minutes())

	if remainingMinutes > extendWindow {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "NOT_IN_WINDOW",
			"message": fmt.Sprintf("剩余时间需小于%d分钟才能续期，当前剩余%d分钟", extendWindow, remainingMinutes),
		})
		return
	}

	result, err := db.Exec(fmt.Sprintf(`
		UPDATE team_instances SET expires_at = expires_at + INTERVAL '%d minutes', updated_at = CURRENT_TIMESTAMP
		WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'`, extendTTL),
		teamID.Int64, challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NO_INSTANCE", "message": "队伍没有运行中的实例"})
		return
	}

	var expiresAt time.Time
	db.QueryRow(`SELECT expires_at FROM team_instances WHERE team_id = $1 AND challenge_id = $2`, teamID.Int64, challengeID).Scan(&expiresAt)

	expiresLocal = expiresAt
	if expiresAt.Location() == time.UTC {
		expiresLocal = time.Date(expiresAt.Year(), expiresAt.Month(), expiresAt.Day(),
			expiresAt.Hour(), expiresAt.Minute(), expiresAt.Second(), expiresAt.Nanosecond(), loc)
	}
	ttl := int(expiresLocal.Sub(now).Seconds())
	if ttl < 0 {
		ttl = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   fmt.Sprintf("已延长%d分钟", extendTTL),
		"expiresAt": expiresAt.Format("2006-01-02 15:04:05"),
		"ttl":       ttl,
	})

	// 记录容器续期日志
	clientIP := c.ClientIP()
	contestID := c.Param("id")
	contestIDInt, _ := strconv.ParseInt(contestID, 10, 64)
	challengeIDInt, _ := strconv.ParseInt(challengeID, 10, 64)
	var displayName, challengeName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName)
	db.QueryRow(`SELECT q.title FROM question_bank q JOIN contest_challenges cc ON q.id = cc.question_id WHERE cc.id = $1`, challengeID).Scan(&challengeName)
	logs.WriteLog(db, logs.TypeContainerExtend, logs.LevelInfo, &userID, &teamID.Int64, &contestIDInt, &challengeIDInt, clientIP,
		displayName+" 续期题目 ["+challengeName+"] 的容器实例", map[string]interface{}{
			"extendMinutes": extendTTL,
		})
}
