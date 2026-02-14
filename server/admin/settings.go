// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package admin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// SystemSettings Docker容器策略设置
type SystemSettings struct {
	ContainerInitialTTL   int  `json:"containerInitialTtl"`   // 初始有效期(分钟)
	ContainerExtendTTL    int  `json:"containerExtendTtl"`    // 单次续期时长(分钟)
	ContainerExtendWindow int  `json:"containerExtendWindow"` // 续期窗口(剩余分钟数)
	AutoDestroyExpired    bool `json:"autoDestroyExpired"`    // 自动销毁过期实例
	PortRangeStart        int  `json:"portRangeStart"`        // 端口范围起始
	PortRangeEnd          int  `json:"portRangeEnd"`          // 端口范围结束
}

// HandleGetSystemSettings 获取系统设置
func HandleGetSystemSettings(c *gin.Context, db *sql.DB) {
	settings := SystemSettings{
		ContainerInitialTTL:   120, // 默认值
		ContainerExtendTTL:    120,
		ContainerExtendWindow: 15,
		AutoDestroyExpired:    true,
		PortRangeStart:        49152, // 默认端口范围（IANA动态端口）
		PortRangeEnd:          65535,
	}

	rows, err := db.Query(`SELECT key, value FROM system_settings WHERE key IN ('container_initial_ttl', 'container_extend_ttl', 'container_extend_window', 'auto_destroy_expired', 'port_range_start', 'port_range_end')`)
	if err != nil {
		c.JSON(http.StatusOK, settings) // 返回默认值
		return
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		switch key {
		case "container_initial_ttl":
			if v, err := strconv.Atoi(value); err == nil {
				settings.ContainerInitialTTL = v
			}
		case "container_extend_ttl":
			if v, err := strconv.Atoi(value); err == nil {
				settings.ContainerExtendTTL = v
			}
		case "container_extend_window":
			if v, err := strconv.Atoi(value); err == nil {
				settings.ContainerExtendWindow = v
			}
		case "auto_destroy_expired":
			settings.AutoDestroyExpired = value == "true"
		case "port_range_start":
			if v, err := strconv.Atoi(value); err == nil {
				settings.PortRangeStart = v
			}
		case "port_range_end":
			if v, err := strconv.Atoi(value); err == nil {
				settings.PortRangeEnd = v
			}
		}
	}

	c.JSON(http.StatusOK, settings)
}

// HandleUpdateSystemSettings 更新系统设置
func HandleUpdateSystemSettings(c *gin.Context, db *sql.DB) {
	var req SystemSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 更新各个设置项
	updates := map[string]string{
		"container_initial_ttl":   strconv.Itoa(req.ContainerInitialTTL),
		"container_extend_ttl":    strconv.Itoa(req.ContainerExtendTTL),
		"container_extend_window": strconv.Itoa(req.ContainerExtendWindow),
		"auto_destroy_expired":    strconv.FormatBool(req.AutoDestroyExpired),
		"port_range_start":        strconv.Itoa(req.PortRangeStart),
		"port_range_end":          strconv.Itoa(req.PortRangeEnd),
	}

	for key, value := range updates {
		_, err := db.Exec(`
			INSERT INTO system_settings (key, value, updated_at) VALUES ($1, $2, CURRENT_TIMESTAMP)
			ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = CURRENT_TIMESTAMP`,
			key, value)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR", "message": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "设置已保存"})
}

// GetSystemSetting 获取单个系统设置值
func GetSystemSetting(db *sql.DB, key string, defaultValue string) string {
	var value string
	err := db.QueryRow(`SELECT value FROM system_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetContainerTTL 获取容器初始有效期（分钟）
func GetContainerTTL(db *sql.DB) int {
	value := GetSystemSetting(db, "container_initial_ttl", "120")
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}
	return 120
}

// GetContainerExtendTTL 获取容器续期时长（分钟）
func GetContainerExtendTTL(db *sql.DB) int {
	value := GetSystemSetting(db, "container_extend_ttl", "120")
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}
	return 120
}

// GetContainerExtendWindow 获取续期窗口（分钟）
func GetContainerExtendWindow(db *sql.DB) int {
	value := GetSystemSetting(db, "container_extend_window", "15")
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}
	return 15
}

// IsAutoDestroyExpiredEnabled 检查是否启用自动销毁
func IsAutoDestroyExpiredEnabled(db *sql.DB) bool {
	value := GetSystemSetting(db, "auto_destroy_expired", "true")
	return value == "true"
}

// GetPortRange 获取端口范围配置
func GetPortRange(db *sql.DB) (int, int) {
	startStr := GetSystemSetting(db, "port_range_start", "49152")
	endStr := GetSystemSetting(db, "port_range_end", "65535")
	start, _ := strconv.Atoi(startStr)
	end, _ := strconv.Atoi(endStr)
	if start <= 0 {
		start = 49152
	}
	if end <= 0 || end <= start {
		end = 65535
	}
	return start, end
}

// AllocatePort 从端口池分配一个可用端口
func AllocatePort(db *sql.DB) (int, error) {
	start, end := GetPortRange(db)
	
	// 查询所有正在使用的端口
	usedPorts := make(map[int]bool)
	rows, err := db.Query(`SELECT ports FROM team_instances WHERE status = 'running' AND ports IS NOT NULL AND ports != ''`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var portsJSON string
			if err := rows.Scan(&portsJSON); err == nil {
				// 解析端口JSON {"80": "10001", "8080": "10002"}
				var portMap map[string]string
				if json.Unmarshal([]byte(portsJSON), &portMap) == nil {
					for _, hostPort := range portMap {
						if p, err := strconv.Atoi(hostPort); err == nil {
							usedPorts[p] = true
						}
					}
				}
			}
		}
	}
	
	// 从范围内找一个可用端口
	for port := start; port <= end; port++ {
		if !usedPorts[port] {
			return port, nil
		}
	}
	
	return 0, fmt.Errorf("no available port in range %d-%d", start, end)
}

// isPortAvailable 检测端口是否被系统占用
func isPortAvailable(port int) bool {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// AllocatePorts 批量分配端口
func AllocatePorts(db *sql.DB, count int) ([]int, error) {
	start, end := GetPortRange(db)
	
	// 查询所有正在使用的端口（平台管理的容器）
	usedPorts := make(map[int]bool)
	
	// 查询 Jeopardy 模式容器端口
	rows, err := db.Query(`SELECT ports FROM team_instances WHERE status = 'running' AND ports IS NOT NULL AND ports != ''`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var portsJSON string
			if err := rows.Scan(&portsJSON); err == nil {
				var portMap map[string]string
				if json.Unmarshal([]byte(portsJSON), &portMap) == nil {
					for _, hostPort := range portMap {
						if p, err := strconv.Atoi(hostPort); err == nil {
							usedPorts[p] = true
						}
					}
				}
			}
		}
	}
	
	// 查询 AWD-F 模式容器端口（包含 running 和 starting 状态）
	rowsAWDF, err := db.Query(`SELECT ports FROM team_instances_awdf WHERE status IN ('running', 'starting') AND ports IS NOT NULL AND ports != ''`)
	if err == nil {
		defer rowsAWDF.Close()
		for rowsAWDF.Next() {
			var portsJSON string
			if err := rowsAWDF.Scan(&portsJSON); err == nil {
				var portMap map[string]string
				if json.Unmarshal([]byte(portsJSON), &portMap) == nil {
					for _, hostPort := range portMap {
						if p, err := strconv.Atoi(hostPort); err == nil {
							usedPorts[p] = true
						}
					}
				}
			}
		}
	}
	
	// 查询已预分配的端口（contest_teams.allocated_ports）
	rowsAlloc, err := db.Query(`SELECT allocated_ports FROM contest_teams WHERE allocated_ports IS NOT NULL AND array_length(allocated_ports, 1) > 0`)
	if err == nil {
		defer rowsAlloc.Close()
		for rowsAlloc.Next() {
			var portsArray []byte
			if err := rowsAlloc.Scan(&portsArray); err == nil && len(portsArray) > 2 {
				// 解析 PostgreSQL 数组格式: {1,2,3}
				portsStr := string(portsArray[1 : len(portsArray)-1])
				if portsStr != "" {
					parts := strings.Split(portsStr, ",")
					for _, p := range parts {
						if port, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
							usedPorts[port] = true
						}
					}
				}
			}
		}
	}
	
	// 分配指定数量的端口（同时检测系统端口占用）
	allocated := make([]int, 0, count)
	for port := start; port <= end && len(allocated) < count; port++ {
		// 跳过平台已分配的端口
		if usedPorts[port] {
			continue
		}
		// 检测系统是否占用该端口
		if !isPortAvailable(port) {
			continue
		}
		allocated = append(allocated, port)
		usedPorts[port] = true // 标记为已使用，避免同一批次重复分配
	}
	
	if len(allocated) < count {
		return nil, fmt.Errorf("not enough available ports, need %d but only got %d", count, len(allocated))
	}
	
	return allocated, nil
}

// CleanupExpiredInstances 清理过期容器实例
func CleanupExpiredInstances(db *sql.DB) {
	if !IsAutoDestroyExpiredEnabled(db) {
		return
	}

	// 查询所有过期且还在运行的实例
	rows, err := db.Query(`
		SELECT id, container_id, team_id, challenge_id 
		FROM team_instances 
		WHERE status = 'running' AND expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var containerID string
		var teamID int64
		var challengeID string
		if err := rows.Scan(&id, &containerID, &teamID, &challengeID); err != nil {
			continue
		}

		// 销毁容器
		exec.Command("docker", "rm", "-f", containerID).Run()

		// 更新数据库状态
		db.Exec(`UPDATE team_instances SET status = 'expired', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	}
}

// StartCleanupScheduler 启动定期清理任务
func StartCleanupScheduler(db *sql.DB) {
	ticker := time.NewTicker(1 * time.Minute) // 每分钟检查一次
	go func() {
		for range ticker.C {
			CleanupExpiredInstances(db)
		}
	}()
}
