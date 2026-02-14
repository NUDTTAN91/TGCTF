// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

var (
	schedulerMutex sync.Mutex
	schedulerMap   = make(map[int64]*AttackScheduler) // contestID -> scheduler
)

// AddMonitorEventFunc 大屏事件记录函数（由 main.go 注入，避免循环依赖）
var AddMonitorEventFunc func(db *sql.DB, contestID string, eventType string, teamName string, userName string, challengeName string)

// BroadcastRankingsFunc 广播排行榜更新函数（由 main.go 注入）
var BroadcastRankingsFunc func(db *sql.DB, contestID string)

// AttackScheduler AWD-F 全局攻击调度器（统一倒计时）
type AttackScheduler struct {
	ContestID        int64
	DB               *sql.DB
	StopChan         chan struct{}
	TriggerChan      chan struct{} // 手动触发下一轮
	Running          bool
	CurrentRound     int
	DefenseInterval  int // 全局防守间隔（秒）
	JudgeConcurrency int // 并发判题数
	NextAttackTime   time.Time
	Judging          bool // 是否正在判题
	mutex            sync.Mutex
}

// StartAttackScheduler 启动比赛的攻击调度器
func StartAttackScheduler(db *sql.DB, contestID int64) {
	schedulerMutex.Lock()
	defer schedulerMutex.Unlock()

	// 检查是否已经在运行
	if s, exists := schedulerMap[contestID]; exists && s.Running {
		log.Printf("[AWD-F] 比赛 %d 的调度器已在运行", contestID)
		return
	}

	// 获取比赛的全局防守配置
	var defenseInterval, judgeConcurrency int
	err := db.QueryRow(`
		SELECT COALESCE(defense_interval, 60), COALESCE(judge_concurrency, 5) 
		FROM contests WHERE id = $1
	`, contestID).Scan(&defenseInterval, &judgeConcurrency)
	if err != nil {
		log.Printf("[AWD-F] 获取比赛配置失败: %v", err)
		defenseInterval = 60
		judgeConcurrency = 5
	}

	scheduler := &AttackScheduler{
		ContestID:        contestID,
		DB:               db,
		StopChan:         make(chan struct{}),
		TriggerChan:      make(chan struct{}, 1),
		Running:          true,
		DefenseInterval:  defenseInterval,
		JudgeConcurrency: judgeConcurrency,
	}

	// 获取当前最大轮次
	db.QueryRow(`
		SELECT COALESCE(MAX(round_number), 0) FROM awdf_rounds WHERE contest_id = $1
	`, contestID).Scan(&scheduler.CurrentRound)

	// 检查是否有未完成的轮次
	var lastRoundTime sql.NullTime
	db.QueryRow(`
		SELECT completed_at FROM awdf_rounds 
		WHERE contest_id = $1 AND round_number = $2
	`, contestID, scheduler.CurrentRound).Scan(&lastRoundTime)

	now := time.Now()
	loc := now.Location() // 获取本地时区
	if lastRoundTime.Valid {
		// 将数据库时间转换为本地时区（pgx驱动会把 TIMESTAMP WITHOUT TIME ZONE 当作UTC解析）
		// 但实际上数据库存的是本地时间，所以需要调整
		lastTime := lastRoundTime.Time
		if lastTime.Location() == time.UTC {
			// 数据库存的是本地时间，但被当作UTC解析了，需要改为本地时区
			lastTime = time.Date(
				lastTime.Year(), lastTime.Month(), lastTime.Day(),
				lastTime.Hour(), lastTime.Minute(), lastTime.Second(),
				lastTime.Nanosecond(), loc,
			)
		}
		// 从上次完成时间开始计算下一次攻击时间
		nextTime := lastTime.Add(time.Duration(defenseInterval) * time.Second)
		if nextTime.Before(now) {
			// 已经过了计划时间，立即开始
			scheduler.NextAttackTime = now.Add(time.Duration(defenseInterval) * time.Second)
			log.Printf("[AWD-F] 调度器重启，上次轮次已过期，立即开始新倒计时")
		} else {
			scheduler.NextAttackTime = nextTime
		}
	} else {
		// 立即开始第一轮
		scheduler.NextAttackTime = now.Add(time.Duration(defenseInterval) * time.Second)
	}

	schedulerMap[contestID] = scheduler

	go scheduler.run()
	log.Printf("[AWD-F] 启动比赛 %d 的调度器，防守间隔: %d秒，并发数: %d", 
		contestID, defenseInterval, judgeConcurrency)
}

// StopAttackScheduler 停止比赛的攻击调度器
func StopAttackScheduler(contestID int64) {
	schedulerMutex.Lock()
	defer schedulerMutex.Unlock()

	if scheduler, exists := schedulerMap[contestID]; exists && scheduler.Running {
		close(scheduler.StopChan)
		scheduler.Running = false
		delete(schedulerMap, contestID)
		log.Printf("[AWD-F] 停止比赛 %d 的攻击调度器", contestID)
	}
}

// TriggerNextRound 手动触发下一轮攻击
func TriggerNextRound(contestID int64) bool {
	schedulerMutex.Lock()
	scheduler, exists := schedulerMap[contestID]
	schedulerMutex.Unlock()

	if !exists || !scheduler.Running {
		return false
	}

	select {
	case scheduler.TriggerChan <- struct{}{}:
		log.Printf("[AWD-F] 比赛 %d 手动触发下一轮攻击", contestID)
		return true
	default:
		return false // 已有触发等待中
	}
}

// run 调度器主循环（全局统一倒计时）
func (s *AttackScheduler) run() {
	ticker := time.NewTicker(1 * time.Second) // 每秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-s.StopChan:
			return
		case <-s.TriggerChan:
			// 手动触发，立即执行
			s.runGlobalAttackRound()
		case <-ticker.C:
			// 检查是否到达攻击时间
			if !s.Judging && time.Now().After(s.NextAttackTime) {
				s.runGlobalAttackRound()
			}
		}
	}
}

// runGlobalAttackRound 执行全局攻击轮次（所有题目统一）
func (s *AttackScheduler) runGlobalAttackRound() {
	s.mutex.Lock()
	if s.Judging {
		s.mutex.Unlock()
		return
	}
	s.Judging = true
	s.mutex.Unlock()

	defer func() {
		s.mutex.Lock()
		s.Judging = false
		s.mutex.Unlock()
	}()

	// 检查比赛状态
	var status, mode string
	err := s.DB.QueryRow("SELECT status, mode FROM contests WHERE id = $1", s.ContestID).Scan(&status, &mode)
	if err != nil || status != "running" || mode != "awd-f" {
		if status == "ended" {
			StopAttackScheduler(s.ContestID)
		}
		return
	}

	// 增加轮次
	s.CurrentRound++
	roundNumber := s.CurrentRound

	// 创建轮次记录
	var roundID int64
	err = s.DB.QueryRow(`
		INSERT INTO awdf_rounds (contest_id, round_number, started_at, status)
		VALUES ($1, $2, NOW(), 'running')
		ON CONFLICT (contest_id, round_number) DO UPDATE SET started_at = NOW(), status = 'running'
		RETURNING id
	`, s.ContestID, roundNumber).Scan(&roundID)
	if err != nil {
		log.Printf("[AWD-F] 创建轮次记录失败: %v", err)
	}

	log.Printf("[AWD-F] 比赛 %d 开始第 %d 轮全局攻击", s.ContestID, roundNumber)

	// 记录轮次开始事件到大屏
	if AddMonitorEventFunc != nil {
		AddMonitorEventFunc(s.DB, fmt.Sprintf("%d", s.ContestID), "round_start", fmt.Sprintf("第 %d 轮", roundNumber), "", "")
	}

	// 获取所有公开的AWD-F题目（有EXP脚本的）
	challengeRows, err := s.DB.Query(`
		SELECT cc.id, q.title
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.contest_id = $1 AND cc.status = 'public'
		AND q.exp_script IS NOT NULL AND q.exp_script != ''
	`, s.ContestID)
	if err != nil {
		log.Printf("[AWD-F] 获取题目失败: %v", err)
		return
	}

	type ChallengeInfo struct {
		ID    int64
		Title string
	}
	var challenges []ChallengeInfo
	for challengeRows.Next() {
		var c ChallengeInfo
		challengeRows.Scan(&c.ID, &c.Title)
		challenges = append(challenges, c)
	}
	challengeRows.Close()

	if len(challenges) == 0 {
		log.Printf("[AWD-F] 比赛 %d 没有配置EXP脚本的题目", s.ContestID)
		s.NextAttackTime = time.Now().Add(time.Duration(s.DefenseInterval) * time.Second)
		return
	}

	// 创建一个 map 保存题目信息（用于事件记录）
	challengeMap := make(map[int64]string)
	for _, ch := range challenges {
		challengeMap[ch.ID] = ch.Title
	}

	// 收集所有需要判定的任务（队伍+题目）
	type JudgeTask struct {
		TeamID      int64
		TeamName    string
		ChallengeID int64
	}
	var tasks []JudgeTask

	for _, ch := range challenges {
		// 获取所有有运行中容器的队伍
		rows, err := s.DB.Query(`
			SELECT DISTINCT ti.team_id, t.name 
			FROM team_instances_awdf ti
			JOIN contest_teams ct ON ti.team_id = ct.team_id AND ti.contest_id = ct.contest_id
			JOIN teams t ON ti.team_id = t.id
			WHERE ti.contest_id = $1 AND ti.challenge_id = $2 AND ti.status = 'running'
			AND ct.status = 'approved'
		`, s.ContestID, ch.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var teamID int64
			var teamName string
			rows.Scan(&teamID, &teamName)
			tasks = append(tasks, JudgeTask{TeamID: teamID, TeamName: teamName, ChallengeID: ch.ID})
		}
		rows.Close()
	}

	totalTasks := len(tasks)
	if totalTasks == 0 {
		log.Printf("[AWD-F] 比赛 %d 第 %d 轮没有需要判定的容器", s.ContestID, roundNumber)
		s.NextAttackTime = time.Now().Add(time.Duration(s.DefenseInterval) * time.Second)
		return
	}

	// 更新轮次总数
	s.DB.Exec(`UPDATE awdf_rounds SET teams_total = $1 WHERE id = $2`, totalTasks, roundID)

	log.Printf("[AWD-F] 比赛 %d 第 %d 轮共 %d 个判定任务，并发数: %d", 
		s.ContestID, roundNumber, totalTasks, s.JudgeConcurrency)

	// 使用带限制的并发执行
	semaphore := make(chan struct{}, s.JudgeConcurrency)
	var wg sync.WaitGroup
	var judgedCount int
	var countMutex sync.Mutex

	for _, task := range tasks {
		wg.Add(1)
		go func(t JudgeTask) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			result, err := RunEXPAttack(s.DB, s.ContestID, t.ChallengeID, t.TeamID, roundNumber)
			if err != nil {
				log.Printf("[AWD-F] 队伍 %d 题目 %d 判定失败: %v", t.TeamID, t.ChallengeID, err)
			} else {
				statusStr := "被攻破"
				if result.DefenseSuccess {
					statusStr = "防守成功"
				}
				log.Printf("[AWD-F] 队伍 %d 题目 %d %s (得分: %d)", t.TeamID, t.ChallengeID, statusStr, result.ScoreEarned)

				// 记录攻防事件到大屏
				if AddMonitorEventFunc != nil {
					challengeTitle := challengeMap[t.ChallengeID]
					if result.DefenseSuccess {
						AddMonitorEventFunc(s.DB, fmt.Sprintf("%d", s.ContestID), "defense_success", t.TeamName, fmt.Sprintf("+%d", result.ScoreEarned), challengeTitle)
					} else {
						AddMonitorEventFunc(s.DB, fmt.Sprintf("%d", s.ContestID), "attack_success", t.TeamName, "", challengeTitle)
					}
				}
			}

			// 更新进度
			countMutex.Lock()
			judgedCount++
			s.DB.Exec(`UPDATE awdf_rounds SET teams_judged = $1 WHERE id = $2`, judgedCount, roundID)
			countMutex.Unlock()
		}(task)
	}

	wg.Wait()

	// 完成轮次
	s.DB.Exec(`UPDATE awdf_rounds SET completed_at = NOW(), status = 'completed' WHERE id = $1`, roundID)

	log.Printf("[AWD-F] 比赛 %d 第 %d 轮攻击完成，共判定 %d 个任务", s.ContestID, roundNumber, totalTasks)

	// 广播排行榜更新（一轮完成后统一推送）
	if BroadcastRankingsFunc != nil {
		BroadcastRankingsFunc(s.DB, fmt.Sprintf("%d", s.ContestID))
	}

	// 设置下一轮攻击时间
	s.NextAttackTime = time.Now().Add(time.Duration(s.DefenseInterval) * time.Second)
	log.Printf("[AWD-F] 比赛 %d 下一轮攻击时间: %s", s.ContestID, s.NextAttackTime.Format("15:04:05"))
}

// CheckAndStartSchedulers 检查并启动所有进行中的AWD-F比赛调度器
func CheckAndStartSchedulers(db *sql.DB) {
	rows, err := db.Query(`
		SELECT id FROM contests WHERE mode = 'awd-f' AND status = 'running'
	`)
	if err != nil {
		log.Printf("[AWD-F] 检查比赛失败: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var contestID int64
		rows.Scan(&contestID)
		StartAttackScheduler(db, contestID)
	}
}

// GetSchedulerStatus 获取调度器状态（用于前端显示）
func GetSchedulerStatus(contestID int64) map[string]interface{} {
	schedulerMutex.Lock()
	defer schedulerMutex.Unlock()

	if scheduler, exists := schedulerMap[contestID]; exists {
		nextAttackSeconds := int(time.Until(scheduler.NextAttackTime).Seconds())
		if nextAttackSeconds < 0 {
			nextAttackSeconds = 0
		}
		return map[string]interface{}{
			"running":           scheduler.Running,
			"currentRound":      scheduler.CurrentRound,
			"defenseInterval":   scheduler.DefenseInterval,
			"judgeConcurrency":  scheduler.JudgeConcurrency,
			"nextAttackSeconds": nextAttackSeconds,
			"judging":           scheduler.Judging,
		}
	}
	return map[string]interface{}{
		"running":           false,
		"currentRound":      0,
		"defenseInterval":   0,
		"judgeConcurrency":  0,
		"nextAttackSeconds": 0,
		"judging":           false,
	}
}

// UpdateDefenseInterval 更新防守间隔（比赛进行中可调整）
func UpdateDefenseInterval(contestID int64, interval int) bool {
	schedulerMutex.Lock()
	scheduler, exists := schedulerMap[contestID]
	schedulerMutex.Unlock()

	if !exists || !scheduler.Running {
		return false
	}

	scheduler.mutex.Lock()
	scheduler.DefenseInterval = interval
	scheduler.mutex.Unlock()

	log.Printf("[AWD-F] 比赛 %d 更新防守间隔为 %d 秒", contestID, interval)
	return true
}
