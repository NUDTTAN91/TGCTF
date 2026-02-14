// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// AWDFContainerManager AWD-F 容器管理器
type AWDFContainerManager struct {
	DB        *sql.DB
	ContestID int64
	mutex     sync.Mutex
}

// AddContainerEventFunc 容器事件记录函数（由 main.go 注入）
var AddContainerEventFunc func(db *sql.DB, contestID string, eventType string, teamName string, userName string, challengeName string)

// 端口分配函数（从 docker 包引入）
var AllocatePortsFunc func(db *sql.DB, count int) ([]int, error)

// 容器 TTL 获取函数
var GetContainerTTLFunc func(db *sql.DB) int

// 批量启动进度回调
type ProgressCallback func(current, total int, teamName, challengeName string, success bool, err string)

// generateSSHPassword 生成16位随机密码（大小写字母+数字+特殊符号）
func generateSSHPassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	password := make([]byte, 16)
	for i := range password {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		password[i] = charset[n.Int64()]
	}
	return string(password)
}

// PreAllocatePortsForContest 比赛开始前为所有队伍预分配端口
func PreAllocatePortsForContest(db *sql.DB, contestID int64) error {
	log.Printf("[AWD-F] 开始为比赛 %d 预分配端口", contestID)

	// 1. 获取比赛容器限制数
	var containerLimit int
	err := db.QueryRow(`SELECT COALESCE(container_limit, 1) FROM contests WHERE id = $1`, contestID).Scan(&containerLimit)
	if err != nil {
		return fmt.Errorf("获取比赛配置失败: %v", err)
	}
	if containerLimit <= 0 {
		containerLimit = 1
	}

	// 2. 获取所有已审核通过的队伍
	teamRows, err := db.Query(`
		SELECT ct.team_id, t.name, ct.allocated_ports
		FROM contest_teams ct
		JOIN teams t ON ct.team_id = t.id
		WHERE ct.contest_id = $1 AND ct.status = 'approved'
		ORDER BY ct.team_id
	`, contestID)
	if err != nil {
		return fmt.Errorf("获取队伍列表失败: %v", err)
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
		// 解析 PostgreSQL 数组格式: {1,2,3}
		if len(portsArray) > 2 {
			portsStr := string(portsArray[1 : len(portsArray)-1]) // 去掉 { }
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
		log.Printf("[AWD-F] 比赛 %d 没有已审核的队伍", contestID)
		return nil
	}

	// 3. 获取所有公开题目的端口需求，计算最大端口数
	challengeRows, err := db.Query(`
		SELECT q.ports
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.contest_id = $1 AND cc.status = 'public' AND q.docker_image IS NOT NULL AND q.docker_image != ''
	`, contestID)
	if err != nil {
		return fmt.Errorf("获取题目列表失败: %v", err)
	}
	defer challengeRows.Close()

	maxPortsPerChallenge := 0
	for challengeRows.Next() {
		var portsJSON sql.NullString
		challengeRows.Scan(&portsJSON)
		if portsJSON.Valid && portsJSON.String != "" {
			var portList []string
			json.Unmarshal([]byte(portsJSON.String), &portList)
			if len(portList) > maxPortsPerChallenge {
				maxPortsPerChallenge = len(portList)
			}
		}
	}

	if maxPortsPerChallenge == 0 {
		log.Printf("[AWD-F] 比赛 %d 题目无端口需求", contestID)
		return nil
	}

	// 4. 计算每个队伍需要的端口数
	portsPerTeam := maxPortsPerChallenge * containerLimit
	log.Printf("[AWD-F] 比赛 %d: 每队伍需要 %d 个端口 (最大%d端口/题 × %d容器限制)", 
		contestID, portsPerTeam, maxPortsPerChallenge, containerLimit)

	// 5. 为每个队伍分配端口
	for _, team := range teams {
		// 检查是否已有足够端口
		if len(team.AllocatedPorts) >= portsPerTeam {
			log.Printf("[AWD-F] 队伍 %s 已有 %d 个端口，无需追加", team.Name, len(team.AllocatedPorts))
			continue
		}

		// 需要追加的端口数
		needed := portsPerTeam - len(team.AllocatedPorts)
		if AllocatePortsFunc == nil {
			return fmt.Errorf("端口分配函数未初始化")
		}

		newPorts, err := AllocatePortsFunc(db, needed)
		if err != nil {
			return fmt.Errorf("为队伍 %s 分配端口失败: %v", team.Name, err)
		}

		// 合并旧端口和新端口
		allPorts := append(team.AllocatedPorts, newPorts...)

		// 更新数据库
		portsStr := "{" + strings.Trim(strings.Join(strings.Fields(fmt.Sprint(allPorts)), ","), "[]") + "}"
		_, err = db.Exec(`UPDATE contest_teams SET allocated_ports = $1, updated_at = NOW() WHERE contest_id = $2 AND team_id = $3`,
			portsStr, contestID, team.ID)
		if err != nil {
			return fmt.Errorf("保存队伍 %s 端口失败: %v", team.Name, err)
		}

		log.Printf("[AWD-F] 队伍 %s 分配端口: %v", team.Name, allPorts)
	}

	log.Printf("[AWD-F] 比赛 %d 端口预分配完成", contestID)
	return nil
}

// StartAllContainersForContest 比赛开始时批量启动所有队伍所有题目的容器
func StartAllContainersForContest(db *sql.DB, contestID int64, callback ProgressCallback) error {
	log.Printf("[AWD-F] 开始为比赛 %d 批量启动容器", contestID)

	// 第一步：预分配端口
	err := PreAllocatePortsForContest(db, contestID)
	if err != nil {
		log.Printf("[AWD-F] 比赛 %d 端口预分配失败: %v", contestID, err)
		return fmt.Errorf("端口预分配失败: %v", err)
	}

	// 获取所有已审核通过的队伍
	teamRows, err := db.Query(`
		SELECT ct.team_id, t.name 
		FROM contest_teams ct
		JOIN teams t ON ct.team_id = t.id
		WHERE ct.contest_id = $1 AND ct.status = 'approved'
		ORDER BY ct.team_id
	`, contestID)
	if err != nil {
		return fmt.Errorf("获取队伍列表失败: %v", err)
	}

	type TeamInfo struct {
		ID   int64
		Name string
	}
	var teams []TeamInfo
	for teamRows.Next() {
		var t TeamInfo
		teamRows.Scan(&t.ID, &t.Name)
		teams = append(teams, t)
	}
	teamRows.Close()

	if len(teams) == 0 {
		log.Printf("[AWD-F] 比赛 %d 没有已审核的队伍", contestID)
		return nil
	}

	// 获取所有公开的 AWD-F 题目
	challengeRows, err := db.Query(`
		SELECT cc.id, q.title, q.docker_image, q.ports, q.cpu_limit, q.memory_limit, q.flag_env, q.flag_script
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.contest_id = $1 AND cc.status = 'public' AND q.docker_image IS NOT NULL AND q.docker_image != ''
		ORDER BY cc.display_order, cc.id
	`, contestID)
	if err != nil {
		return fmt.Errorf("获取题目列表失败: %v", err)
	}

	type ChallengeInfo struct {
		ID          int64
		Title       string
		DockerImage string
		Ports       sql.NullString
		CPULimit    sql.NullString
		MemoryLimit sql.NullString
		FlagEnv     sql.NullString
		FlagScript  sql.NullString
	}
	var challenges []ChallengeInfo
	for challengeRows.Next() {
		var ch ChallengeInfo
		challengeRows.Scan(&ch.ID, &ch.Title, &ch.DockerImage, &ch.Ports, &ch.CPULimit, &ch.MemoryLimit, &ch.FlagEnv, &ch.FlagScript)
		challenges = append(challenges, ch)
	}
	challengeRows.Close()

	if len(challenges) == 0 {
		log.Printf("[AWD-F] 比赛 %d 没有需要启动容器的题目", contestID)
		return nil
	}

	// 计算总数
	total := len(teams) * len(challenges)
	current := 0

	log.Printf("[AWD-F] 准备启动 %d 个队伍 × %d 道题目 = %d 个容器", len(teams), len(challenges), total)

	// AWD-F 模式：使用比赛结束时间作为容器过期时间（而不是固定 TTL）
	var contestEndTime time.Time
	err = db.QueryRow(`SELECT end_time FROM contests WHERE id = $1`, contestID).Scan(&contestEndTime)
	if err != nil {
		log.Printf("[AWD-F] 获取比赛结束时间失败: %v", err)
		// 回退到默认 24 小时
		contestEndTime = time.Now().Add(24 * time.Hour)
	}

	// 逐个启动容器（串行，避免服务器压力过大）
	for _, team := range teams {
		for _, ch := range challenges {
			current++

			// 检查是否已有运行中的容器
			var existingID int64
			err := db.QueryRow(`
				SELECT id FROM team_instances_awdf 
				WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'
			`, team.ID, ch.ID).Scan(&existingID)
			if err == nil {
				// 已有容器，跳过
				if callback != nil {
					callback(current, total, team.Name, ch.Title, true, "已有运行中容器")
				}
				continue
			}

			// 获取或生成该队伍该题的 Flag
			flag := GetOrCreateAWDFFlag(db, team.ID, contestID, ch.ID)

			// 创建容器（使用比赛结束时间作为过期时间）
			containerID, ports, err := createAWDFContainerWithEndTime(db, team.ID, contestID, ch.ID, ch, flag, contestEndTime)
			if err != nil {
				log.Printf("[AWD-F] 队伍 %s 题目 %s 容器创建失败: %v", team.Name, ch.Title, err)
				if callback != nil {
					callback(current, total, team.Name, ch.Title, false, err.Error())
				}
				continue
			}

			log.Printf("[AWD-F] 队伍 %s 题目 %s 容器创建成功: %s", team.Name, ch.Title, containerID)
			if callback != nil {
				callback(current, total, team.Name, ch.Title, true, fmt.Sprintf("端口: %v", ports))
			}

			// 短暂休眠，避免 Docker 压力过大
			time.Sleep(100 * time.Millisecond)
		}
	}

	log.Printf("[AWD-F] 比赛 %d 批量启动容器完成，共 %d 个", contestID, current)
	return nil
}

// StartContainersForChallenge 题目上架时为所有已审核队伍创建容器
func StartContainersForChallenge(db *sql.DB, contestID, challengeID int64) error {
	log.Printf("[AWD-F] 开始为题目 %d 创建所有队伍的容器", challengeID)

	// 检查比赛是否在进行中
	var contestStatus string
	err := db.QueryRow(`SELECT status FROM contests WHERE id = $1`, contestID).Scan(&contestStatus)
	if err != nil || contestStatus != "running" {
		log.Printf("[AWD-F] 比赛 %d 不在进行中，跳过容器创建", contestID)
		return nil
	}

	// 获取题目配置
	var ch struct {
		ID          int64
		Title       string
		DockerImage string
		Ports       sql.NullString
		CPULimit    sql.NullString
		MemoryLimit sql.NullString
		FlagEnv     sql.NullString
		FlagScript  sql.NullString
	}
	err = db.QueryRow(`
		SELECT cc.id, q.title, q.docker_image, q.ports, q.cpu_limit, q.memory_limit, q.flag_env, q.flag_script
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.id = $1 AND q.docker_image IS NOT NULL AND q.docker_image != ''
	`, challengeID).Scan(&ch.ID, &ch.Title, &ch.DockerImage, &ch.Ports, &ch.CPULimit, &ch.MemoryLimit, &ch.FlagEnv, &ch.FlagScript)
	if err != nil {
		log.Printf("[AWD-F] 题目 %d 没有配置Docker镜像，跳过容器创建", challengeID)
		return nil
	}

	// 获取所有已审核通过的队伍
	teamRows, err := db.Query(`
		SELECT ct.team_id, t.name 
		FROM contest_teams ct
		JOIN teams t ON ct.team_id = t.id
		WHERE ct.contest_id = $1 AND ct.status = 'approved'
		ORDER BY ct.team_id
	`, contestID)
	if err != nil {
		return fmt.Errorf("获取队伍列表失败: %v", err)
	}

	type TeamInfo struct {
		ID   int64
		Name string
	}
	var teams []TeamInfo
	for teamRows.Next() {
		var t TeamInfo
		teamRows.Scan(&t.ID, &t.Name)
		teams = append(teams, t)
	}
	teamRows.Close()

	if len(teams) == 0 {
		log.Printf("[AWD-F] 比赛 %d 没有已审核的队伍", contestID)
		return nil
	}

	// 获取比赛结束时间
	var contestEndTime time.Time
	err = db.QueryRow(`SELECT end_time FROM contests WHERE id = $1`, contestID).Scan(&contestEndTime)
	if err != nil {
		contestEndTime = time.Now().Add(24 * time.Hour)
	}

	log.Printf("[AWD-F] 准备为题目 %s 创建 %d 个队伍的容器", ch.Title, len(teams))

	// 为每个队伍创建容器
	for _, team := range teams {
		// 检查是否已有运行中的容器
		var existingID int64
		err := db.QueryRow(`
			SELECT id FROM team_instances_awdf 
			WHERE team_id = $1 AND challenge_id = $2 AND status = 'running'
		`, team.ID, challengeID).Scan(&existingID)
		if err == nil {
			log.Printf("[AWD-F] 队伍 %s 题目 %s 已有运行中容器，跳过", team.Name, ch.Title)
			continue
		}

		// 获取或生成 Flag
		flag := GetOrCreateAWDFFlag(db, team.ID, contestID, challengeID)

		// 创建容器
		containerID, _, err := createAWDFContainerWithEndTime(db, team.ID, contestID, challengeID, ch, flag, contestEndTime)
		if err != nil {
			log.Printf("[AWD-F] 队伍 %s 题目 %s 容器创建失败: %v", team.Name, ch.Title, err)
			continue
		}

		log.Printf("[AWD-F] 队伍 %s 题目 %s 容器创建成功: %s", team.Name, ch.Title, containerID)
		time.Sleep(100 * time.Millisecond)
	}

	log.Printf("[AWD-F] 题目 %d 容器创建完成", challengeID)
	return nil
}

// StopContainersForChallenge 题目下架时销毁该题目的所有容器
func StopContainersForChallenge(db *sql.DB, contestID, challengeID int64) error {
	log.Printf("[AWD-F] 开始销毁题目 %d 的所有容器", challengeID)

	// 获取该题目所有运行中的容器
	rows, err := db.Query(`
		SELECT id, container_id, container_name 
		FROM team_instances_awdf 
		WHERE contest_id = $1 AND challenge_id = $2 AND status = 'running'
	`, contestID, challengeID)
	if err != nil {
		return fmt.Errorf("获取容器列表失败: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var instanceID int64
		var containerID, containerName string
		rows.Scan(&instanceID, &containerID, &containerName)

		// 销毁容器
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
		cancel()

		// 更新数据库状态
		db.Exec(`UPDATE team_instances_awdf SET status = 'destroyed', updated_at = NOW() WHERE id = $1`, instanceID)
		count++
	}

	log.Printf("[AWD-F] 题目 %d 销毁了 %d 个容器", challengeID, count)
	return nil
}

// StopAllContainersForContest 比赛结束时批量销毁所有容器
func StopAllContainersForContest(db *sql.DB, contestID int64) error {
	log.Printf("[AWD-F] 开始为比赛 %d 批量销毁容器", contestID)

	// 获取所有运行中的容器
	rows, err := db.Query(`
		SELECT id, container_id, container_name 
		FROM team_instances_awdf 
		WHERE contest_id = $1 AND status = 'running'
	`, contestID)
	if err != nil {
		return fmt.Errorf("获取容器列表失败: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var instanceID int64
		var containerID, containerName string
		rows.Scan(&instanceID, &containerID, &containerName)

		// 销毁容器
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
		cancel()

		// 更新数据库状态
		db.Exec(`UPDATE team_instances_awdf SET status = 'destroyed', updated_at = NOW() WHERE id = $1`, instanceID)
		count++
	}

	log.Printf("[AWD-F] 比赛 %d 批量销毁容器完成，共 %d 个", contestID, count)
	return nil
}

// GetOrCreateAWDFFlag 获取或创建 AWD-F 题目的队伍 Flag
func GetOrCreateAWDFFlag(db *sql.DB, teamID, contestID, challengeID int64) string {
	// 先查询是否已有 Flag
	var flag string
	err := db.QueryRow(`
		SELECT flag FROM team_challenge_flags 
		WHERE team_id = $1 AND contest_id = $2 AND challenge_id = $3
	`, teamID, contestID, challengeID).Scan(&flag)
	if err == nil {
		return flag
	}

	// 获取比赛的 Flag 格式
	var flagFormat string
	db.QueryRow(`SELECT COALESCE(flag_format, 'flag{[GUID]}') FROM contests WHERE id = $1`, contestID).Scan(&flagFormat)

	// 生成真正的 UUID 格式 (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
	guid := generateUUID()
	flag = strings.Replace(flagFormat, "[GUID]", guid, 1)

	// 保存 Flag
	db.Exec(`
		INSERT INTO team_challenge_flags (team_id, contest_id, challenge_id, flag)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id, challenge_id) DO UPDATE SET flag = $4
	`, teamID, contestID, challengeID, flag)

	return flag
}

// generateUUID 生成 UUID v4 格式字符串
func generateUUID() string {
	uuid := make([]byte, 16)
	rand.Read(uuid)
	// 设置版本号 (v4) 和变体位
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// createAWDFContainer 创建单个 AWD-F 容器
func createAWDFContainer(db *sql.DB, teamID, contestID, challengeID int64, ch struct {
	ID          int64
	Title       string
	DockerImage string
	Ports       sql.NullString
	CPULimit    sql.NullString
	MemoryLimit sql.NullString
	FlagEnv     sql.NullString
	FlagScript  sql.NullString
}, flag string, ttlSeconds int) (string, map[string]string, error) {

	// 解析端口列表
	var portList []string
	if ch.Ports.Valid && ch.Ports.String != "" {
		json.Unmarshal([]byte(ch.Ports.String), &portList)
	}

	containerName := fmt.Sprintf("tg_team_%d_%d_%d", teamID, challengeID, time.Now().Unix())
	args := []string{"run", "-d", "--name", containerName}

	// 分配端口（优先使用预分配端口）
	portInfo := make(map[string]string)
	if len(portList) > 0 {
		// 查询队伍的预分配端口
		var teamPorts []int
		var portsArray []byte
		err := db.QueryRow(`SELECT allocated_ports FROM contest_teams WHERE contest_id = $1 AND team_id = $2`, contestID, teamID).Scan(&portsArray)
		if err == nil && len(portsArray) > 2 {
			portsStr := string(portsArray[1 : len(portsArray)-1])
			if portsStr != "" {
				parts := strings.Split(portsStr, ",")
				for _, p := range parts {
					if port, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
						teamPorts = append(teamPorts, port)
					}
				}
			}
		}

		if len(teamPorts) >= len(portList) {
			log.Printf("[AWD-F] 队伍 %d 使用预分配端口: %v", teamID, teamPorts[:len(portList)])
			for i, containerPort := range portList {
				hostPort := teamPorts[i]
				args = append(args, "-p", fmt.Sprintf("%d:%s", hostPort, containerPort))
				portInfo[containerPort] = strconv.Itoa(hostPort)
			}
		} else if AllocatePortsFunc != nil {
			log.Printf("[AWD-F] 队伍 %d 预分配端口不足，动态分配", teamID)
			allocatedPorts, err := AllocatePortsFunc(db, len(portList))
			if err != nil {
				return "", nil, fmt.Errorf("端口分配失败: %v", err)
			}
			for i, containerPort := range portList {
				hostPort := allocatedPorts[i]
				args = append(args, "-p", fmt.Sprintf("%d:%s", hostPort, containerPort))
				portInfo[containerPort] = strconv.Itoa(hostPort)
			}
		} else {
			for _, port := range portList {
				args = append(args, "-p", fmt.Sprintf(":%s", port))
			}
		}
	}

	// 资源限制
	if ch.CPULimit.Valid && ch.CPULimit.String != "" {
		args = append(args, "--cpus", ch.CPULimit.String)
	}
	if ch.MemoryLimit.Valid && ch.MemoryLimit.String != "" {
		args = append(args, "-m", ch.MemoryLimit.String)
	}

	// Flag 注入
	useCmdArg := false
	if ch.FlagEnv.Valid && ch.FlagEnv.String != "" {
		envNames := strings.Split(ch.FlagEnv.String, ",")
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

	// 标签
	args = append(args, "--label", "tg.type=awdf")
	args = append(args, "--label", fmt.Sprintf("tg.team_id=%d", teamID))
	args = append(args, "--label", fmt.Sprintf("tg.challenge_id=%d", challengeID))
	args = append(args, "--label", fmt.Sprintf("tg.contest_id=%d", contestID))

	args = append(args, ch.DockerImage)

	// 命令行参数传递 Flag
	if useCmdArg {
		args = append(args, flag)
	}

	// 执行 Docker 命令
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run 失败: %v, output: %s", err, string(output))
	}

	outputLines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containerID := strings.TrimSpace(outputLines[len(outputLines)-1])
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}

	// 如果使用 Docker 自动分配端口，查询端口映射
	if len(portInfo) == 0 && len(portList) > 0 {
		time.Sleep(500 * time.Millisecond)
		portCmd := exec.CommandContext(ctx, "docker", "port", containerID)
		portOutput, _ := portCmd.CombinedOutput()
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
	}

	// 执行 Flag 注入脚本
	if ch.FlagScript.Valid && ch.FlagScript.String != "" {
		time.Sleep(500 * time.Millisecond)
		scriptCmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", ch.FlagScript.String, flag)
		scriptCmd.CombinedOutput() // 忽略错误，不阻塞
	}

	// 保存到数据库
	portsJSON, _ := json.Marshal(portInfo)
	expiresAt := time.Now().Add(time.Duration(ttlSeconds) * time.Second)

	_, err = db.Exec(`
		INSERT INTO team_instances_awdf (team_id, contest_id, challenge_id, container_id, container_name, ports, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'running', $7)
		ON CONFLICT (team_id, challenge_id) DO UPDATE SET
			container_id = $4, container_name = $5, ports = $6, status = 'running', expires_at = $7, updated_at = NOW()
	`, teamID, contestID, challengeID, containerID, containerName, string(portsJSON), expiresAt)

	if err != nil {
		// 创建失败，清理容器
		exec.Command("docker", "rm", "-f", containerID).Run()
		return "", nil, fmt.Errorf("保存数据库失败: %v", err)
	}

	return containerID, portInfo, nil
}

// createAWDFContainerWithEndTime 创建 AWD-F 容器（使用比赛结束时间作为过期时间）
func createAWDFContainerWithEndTime(db *sql.DB, teamID, contestID, challengeID int64, ch struct {
	ID          int64
	Title       string
	DockerImage string
	Ports       sql.NullString
	CPULimit    sql.NullString
	MemoryLimit sql.NullString
	FlagEnv     sql.NullString
	FlagScript  sql.NullString
}, flag string, expiresAt time.Time) (string, map[string]string, error) {

	// 生成 SSH 密码
	sshPassword := generateSSHPassword()

	// 解析端口列表
	var portList []string
	if ch.Ports.Valid && ch.Ports.String != "" {
		json.Unmarshal([]byte(ch.Ports.String), &portList)
	}

	containerName := fmt.Sprintf("tg_team_%d_%d_%d", teamID, challengeID, time.Now().Unix())
	args := []string{"run", "-d", "--name", containerName}

	// 分配端口（优先使用预分配端口）
	portInfo := make(map[string]string)
	if len(portList) > 0 {
		// 查询队伍的预分配端口
		var teamPorts []int
		var portsArray []byte
		err := db.QueryRow(`SELECT allocated_ports FROM contest_teams WHERE contest_id = $1 AND team_id = $2`, contestID, teamID).Scan(&portsArray)
		if err == nil && len(portsArray) > 2 {
			// 解析 PostgreSQL 数组格式: {1,2,3}
			portsStr := string(portsArray[1 : len(portsArray)-1])
			if portsStr != "" {
				parts := strings.Split(portsStr, ",")
				for _, p := range parts {
					if port, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
						teamPorts = append(teamPorts, port)
					}
				}
			}
		}

		if len(teamPorts) >= len(portList) {
			// 使用预分配端口
			log.Printf("[AWD-F] 队伍 %d 使用预分配端口: %v", teamID, teamPorts[:len(portList)])
			for i, containerPort := range portList {
				hostPort := teamPorts[i]
				args = append(args, "-p", fmt.Sprintf("%d:%s", hostPort, containerPort))
				portInfo[containerPort] = strconv.Itoa(hostPort)
			}
		} else if AllocatePortsFunc != nil {
			// Fallback: 动态分配端口
			log.Printf("[AWD-F] 队伍 %d 预分配端口不足，动态分配", teamID)
			allocatedPorts, err := AllocatePortsFunc(db, len(portList))
			if err != nil {
				return "", nil, fmt.Errorf("端口分配失败: %v", err)
			}
			for i, containerPort := range portList {
				hostPort := allocatedPorts[i]
				args = append(args, "-p", fmt.Sprintf("%d:%s", hostPort, containerPort))
				portInfo[containerPort] = strconv.Itoa(hostPort)
			}
		} else {
			// 使用 Docker 自动分配
			for _, port := range portList {
				args = append(args, "-p", fmt.Sprintf(":%s", port))
			}
		}
	}

	// 资源限制
	if ch.CPULimit.Valid && ch.CPULimit.String != "" {
		args = append(args, "--cpus", ch.CPULimit.String)
	}
	if ch.MemoryLimit.Valid && ch.MemoryLimit.String != "" {
		args = append(args, "-m", ch.MemoryLimit.String)
	}

	// Flag 注入
	useCmdArg := false
	if ch.FlagEnv.Valid && ch.FlagEnv.String != "" {
		envNames := strings.Split(ch.FlagEnv.String, ",")
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

	// SSH 密码注入
	args = append(args, "-e", fmt.Sprintf("SSH_PASSWORD=%s", sshPassword))

	// 标签
	args = append(args, "--label", "tg.type=awdf")
	args = append(args, "--label", fmt.Sprintf("tg.team_id=%d", teamID))
	args = append(args, "--label", fmt.Sprintf("tg.challenge_id=%d", challengeID))
	args = append(args, "--label", fmt.Sprintf("tg.contest_id=%d", contestID))

	args = append(args, ch.DockerImage)

	// 命令行参数传递 Flag
	if useCmdArg {
		args = append(args, flag)
	}

	// 执行 Docker 命令
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run 失败: %v, output: %s", err, string(output))
	}

	outputLines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containerID := strings.TrimSpace(outputLines[len(outputLines)-1])
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}

	// 如果使用 Docker 自动分配端口，查询端口映射
	if len(portInfo) == 0 && len(portList) > 0 {
		time.Sleep(500 * time.Millisecond)
		portCmd := exec.CommandContext(ctx, "docker", "port", containerID)
		portOutput, _ := portCmd.CombinedOutput()
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
	}

	// 执行 Flag 注入脚本
	if ch.FlagScript.Valid && ch.FlagScript.String != "" {
		time.Sleep(500 * time.Millisecond)
		scriptCmd := exec.CommandContext(ctx, "docker", "exec", containerID, "sh", ch.FlagScript.String, flag)
		scriptCmd.CombinedOutput() // 忽略错误，不阻塞
	}

	// 保存到数据库（包含 SSH 密码）
	portsJSON, _ := json.Marshal(portInfo)

	_, err = db.Exec(`
		INSERT INTO team_instances_awdf (team_id, contest_id, challenge_id, container_id, container_name, ports, ssh_password, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'running', $8)
		ON CONFLICT (team_id, challenge_id) DO UPDATE SET
			container_id = $4, container_name = $5, ports = $6, ssh_password = $7, status = 'running', expires_at = $8, updated_at = NOW()
	`, teamID, contestID, challengeID, containerID, containerName, string(portsJSON), sshPassword, expiresAt)

	if err != nil {
		// 创建失败，清理容器
		exec.Command("docker", "rm", "-f", containerID).Run()
		return "", nil, fmt.Errorf("保存数据库失败: %v", err)
	}

	return containerID, portInfo, nil
}

// OnContestStatusChange 比赛状态变更钩子函数
var OnContestStatusChange func(db *sql.DB, contestID int64, oldStatus, newStatus, mode string)

// HandleContestStatusChange 处理比赛状态变更
func HandleContestStatusChange(db *sql.DB, contestID int64, oldStatus, newStatus, mode string) {
	log.Printf("[AWD-F] 比赛 %d 状态变更: %s -> %s (mode=%s)", contestID, oldStatus, newStatus, mode)

	// 只处理 AWD-F 模式
	if mode != "awd-f" {
		return
	}

	// 比赛开始：启动所有容器
	if newStatus == "running" && oldStatus != "running" {
		go func() {
			err := StartAllContainersForContest(db, contestID, func(current, total int, teamName, challengeName string, success bool, msg string) {
				status := "成功"
				if !success {
					status = "失败"
				}
				log.Printf("[AWD-F] 启动进度 %d/%d: 队伍[%s] 题目[%s] %s - %s", current, total, teamName, challengeName, status, msg)
			})
			if err != nil {
				log.Printf("[AWD-F] 批量启动容器失败: %v", err)
			}
			// 启动攻击调度器
			StartAttackScheduler(db, contestID)
		}()
	}

	// 比赛结束：销毁所有容器
	if newStatus == "ended" && oldStatus != "ended" {
		go func() {
			// 停止攻击调度器
			StopAttackScheduler(contestID)
			// 销毁所有容器
			err := StopAllContainersForContest(db, contestID)
			if err != nil {
				log.Printf("[AWD-F] 批量销毁容器失败: %v", err)
			}
		}()
	}
}

// ResetTeamContainer 重置队伍的容器（销毁并重建，补丁不保留）
func ResetTeamContainer(db *sql.DB, teamID, contestID, challengeID int64) (map[string]string, error) {
	log.Printf("[AWD-F] 重置容器: 队伍 %d 比赛 %d 题目 %d", teamID, contestID, challengeID)

	// 1. 获取现有容器信息
	var oldContainerID string
	err := db.QueryRow(`
		SELECT container_id FROM team_instances_awdf 
		WHERE team_id = $1 AND contest_id = $2 AND challenge_id = $3 AND status = 'running'
	`, teamID, contestID, challengeID).Scan(&oldContainerID)

	if err == nil && oldContainerID != "" {
		// 2. 销毁旧容器
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		exec.CommandContext(ctx, "docker", "rm", "-f", oldContainerID).Run()
		cancel()

		// 更新数据库状态
		db.Exec(`UPDATE team_instances_awdf SET status = 'destroyed', updated_at = NOW() WHERE team_id = $1 AND challenge_id = $2`, teamID, challengeID)
	}

	// 3. 删除该队伍该题的补丁记录（重置后不保留）
	db.Exec(`DELETE FROM awdf_patches WHERE team_id = $1 AND contest_id = $2 AND challenge_id = $3`, teamID, contestID, challengeID)

	// 4. 获取题目配置
	var ch struct {
		ID          int64
		Title       string
		DockerImage string
		Ports       sql.NullString
		CPULimit    sql.NullString
		MemoryLimit sql.NullString
		FlagEnv     sql.NullString
		FlagScript  sql.NullString
	}
	err = db.QueryRow(`
		SELECT cc.id, q.title, q.docker_image, q.ports, q.cpu_limit, q.memory_limit, q.flag_env, q.flag_script
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.id = $1
	`, challengeID).Scan(&ch.ID, &ch.Title, &ch.DockerImage, &ch.Ports, &ch.CPULimit, &ch.MemoryLimit, &ch.FlagEnv, &ch.FlagScript)
	if err != nil {
		return nil, fmt.Errorf("获取题目配置失败: %v", err)
	}

	if ch.DockerImage == "" {
		return nil, fmt.Errorf("该题目未配置 Docker 镜像")
	}

	// 5. 获取或生成 Flag
	flag := GetOrCreateAWDFFlag(db, teamID, contestID, challengeID)

	// 6. 获取比赛结束时间（AWD-F 模式容器跟随比赛生命周期）
	var contestEndTime time.Time
	err = db.QueryRow(`SELECT end_time FROM contests WHERE id = $1`, contestID).Scan(&contestEndTime)
	if err != nil {
		log.Printf("[AWD-F] 获取比赛结束时间失败: %v", err)
		// 回退到默认 24 小时
		contestEndTime = time.Now().Add(24 * time.Hour)
	}

	// 7. 创建新容器（使用比赛结束时间作为过期时间）
	_, portInfo, err := createAWDFContainerWithEndTime(db, teamID, contestID, challengeID, ch, flag, contestEndTime)
	if err != nil {
		return nil, fmt.Errorf("创建容器失败: %v", err)
	}

	log.Printf("[AWD-F] 容器重置成功: 队伍 %d 题目 %d", teamID, challengeID)
	return portInfo, nil
}

// HandleResetContainer HTTP 处理函数：选手重置容器
func HandleResetContainer(db *sql.DB, c *gin.Context) {
	contestID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	challengeID, _ := strconv.ParseInt(c.Param("challengeId"), 10, 64)

	// 从中间件获取用户ID
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(401, gin.H{"error": "未登录"})
		return
	}
	userID := userIDVal.(int64)

	// 获取用户的队伍 ID
	var teamID sql.NullInt64
	db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(403, gin.H{"error": "您未加入任何队伍"})
		return
	}

	// 检查比赛状态
	var status, mode string
	err := db.QueryRow(`SELECT status, mode FROM contests WHERE id = $1`, contestID).Scan(&status, &mode)
	if err != nil || status != "running" || mode != "awd-f" {
		c.JSON(400, gin.H{"error": "比赛未进行中或不是 AWD-F 模式"})
		return
	}

	// 检查队伍是否已通过审核
	var teamStatus string
	err = db.QueryRow(`SELECT status FROM contest_teams WHERE contest_id = $1 AND team_id = $2`, contestID, teamID.Int64).Scan(&teamStatus)
	if err != nil || teamStatus != "approved" {
		c.JSON(403, gin.H{"error": "队伍未通过审核"})
		return
	}

	// 执行重置
	portInfo, err := ResetTeamContainer(db, teamID.Int64, contestID, challengeID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 获取队伍名和题目名（用于事件记录）
	var teamName, challengeName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
	db.QueryRow(`SELECT q.title FROM contest_challenges_awdf cc JOIN question_bank_awdf q ON cc.question_id = q.id WHERE cc.id = $1`, challengeID).Scan(&challengeName)

	// 记录容器重置事件
	if AddContainerEventFunc != nil {
		AddContainerEventFunc(db, fmt.Sprintf("%d", contestID), "container_reset", teamName, "", challengeName)
	}

	c.JSON(200, gin.H{
		"success": true,
		"message": "容器重置成功，补丁已清除",
		"ports":   portInfo,
	})
}
