// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package logs

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// 日志类型常量
const (
	TypeLogin            = "login"
	TypeLogout           = "logout"
	TypeContainerCreate  = "container_create"
	TypeContainerDestroy = "container_destroy"
	TypeContainerExtend  = "container_extend"
	TypeFlagSubmit       = "flag_submit"
	TypeCheating         = "cheating"
	TypeAdminOp          = "admin_op"
	TypeChallengeView    = "challenge_view"  // 题目首次查看
	TypeAvatarUpdate     = "avatar_update"   // 头像更新
	TypePasswordChange   = "password_change" // 密码修改
)

// 日志级别常量
const (
	LevelInfo    = "info"
	LevelWarning = "warning"
	LevelError   = "error"
	LevelSuccess = "success"
)

// LogEntry 日志条目
type LogEntry struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Level       string          `json:"level"`
	UserID      *int64          `json:"userId,omitempty"`
	UserName    string          `json:"userName,omitempty"`
	TeamID      *int64          `json:"teamId,omitempty"`
	TeamName    string          `json:"teamName,omitempty"`
	ContestID   *int64          `json:"contestId,omitempty"`
	ChallengeID *int64          `json:"challengeId,omitempty"`
	IPAddress   string          `json:"ipAddress,omitempty"`
	Message     string          `json:"message"`
	Details     json.RawMessage `json:"details,omitempty"`
	CreatedAt   string          `json:"createdAt"`
}

// WebSocket 连接管理
var (
	clients   = make(map[*websocket.Conn]bool)
	clientsMu sync.RWMutex
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

// WriteLog 写入日志（供其他模块调用）
func WriteLog(db *sql.DB, logType, level string, userID, teamID, contestID *int64, challengeID *int64, ipAddress, message string, details interface{}) error {
	var detailsJSON []byte
	var err error
	if details != nil {
		detailsJSON, err = json.Marshal(details)
		if err != nil {
			detailsJSON = nil
		}
	}

	_, err = db.Exec(`
		INSERT INTO system_logs (type, level, user_id, team_id, contest_id, challenge_id, ip_address, message, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		logType, level, userID, teamID, contestID, challengeID, ipAddress, message, detailsJSON)

	// 查询用户名和队伍名用于 WebSocket 广播
	var userName, teamName string
	if userID != nil {
		db.QueryRow(`SELECT COALESCE(display_name, username) FROM users WHERE id = $1`, *userID).Scan(&userName)
	}
	if teamID != nil {
		db.QueryRow(`SELECT name FROM teams WHERE id = $1`, *teamID).Scan(&teamName)
	}

	// 实时推送新日志给所有 WebSocket 客户端
	go broadcastLog(LogEntry{
		Type:      logType,
		Level:     level,
		UserID:    userID,
		UserName:  userName,
		TeamID:    teamID,
		TeamName:  teamName,
		IPAddress: ipAddress,
		Message:   message,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	})

	return err
}

// WriteLogSimple 简化版写入日志
func WriteLogSimple(db *sql.DB, logType, level string, userID int64, ipAddress, message string) error {
	return WriteLog(db, logType, level, &userID, nil, nil, nil, ipAddress, message, nil)
}

// HandleGetLogs 获取日志列表（管理后台API）
func HandleGetLogs(c *gin.Context, db *sql.DB) {
	// 分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 10 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	// 过滤参数
	logType := c.Query("type")
	level := c.Query("level")
	search := c.Query("search")

	// 构建查询
	query := `
		SELECT l.id, l.type, l.level, l.user_id, COALESCE(u.display_name, u.username), l.team_id, t.name,
		       l.contest_id, l.challenge_id, l.ip_address, l.message, l.details, l.created_at
		FROM system_logs l
		LEFT JOIN users u ON l.user_id = u.id
		LEFT JOIN teams t ON l.team_id = t.id
		WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM system_logs l WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if logType != "" {
		query += " AND l.type = $" + strconv.Itoa(argIdx)
		countQuery += " AND l.type = $" + strconv.Itoa(argIdx)
		args = append(args, logType)
		argIdx++
	}
	if level != "" {
		query += " AND l.level = $" + strconv.Itoa(argIdx)
		countQuery += " AND l.level = $" + strconv.Itoa(argIdx)
		args = append(args, level)
		argIdx++
	}
	if search != "" {
		query += " AND l.message ILIKE $" + strconv.Itoa(argIdx)
		countQuery += " AND l.message ILIKE $" + strconv.Itoa(argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	// 总数
	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	db.QueryRow(countQuery, countArgs...).Scan(&total)

	// 分页查询
	query += " ORDER BY l.created_at DESC LIMIT $" + strconv.Itoa(argIdx) + " OFFSET $" + strconv.Itoa(argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var log LogEntry
		var userID, teamID, contestID, challengeID sql.NullInt64
		var userName, teamName, ipAddress sql.NullString
		var details []byte
		var createdAt time.Time

		if err := rows.Scan(&log.ID, &log.Type, &log.Level, &userID, &userName, &teamID, &teamName,
			&contestID, &challengeID, &ipAddress, &log.Message, &details, &createdAt); err != nil {
			continue
		}

		if userID.Valid {
			log.UserID = &userID.Int64
		}
		if userName.Valid {
			log.UserName = userName.String
		}
		if teamID.Valid {
			log.TeamID = &teamID.Int64
		}
		if teamName.Valid {
			log.TeamName = teamName.String
		}
		if contestID.Valid {
			log.ContestID = &contestID.Int64
		}
		if challengeID.Valid {
			log.ChallengeID = &challengeID.Int64
		}
		if ipAddress.Valid {
			log.IPAddress = ipAddress.String
		}
		if len(details) > 0 {
			log.Details = details
		}
		log.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		logs = append(logs, log)
	}

	if logs == nil {
		logs = []LogEntry{}
	}

	totalPages := (total + pageSize - 1) / pageSize
	c.JSON(http.StatusOK, gin.H{
		"logs":       logs,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": totalPages,
	})
}

// HandleLogsWebSocket WebSocket 实时日志推送
func HandleLogsWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	clientsMu.Lock()
	clients[conn] = true
	clientsMu.Unlock()

	defer func() {
		clientsMu.Lock()
		delete(clients, conn)
		clientsMu.Unlock()
	}()

	// 保持连接，读取客户端消息（心跳）
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// broadcastLog 广播日志给所有客户端
func broadcastLog(log LogEntry) {
	data, err := json.Marshal(log)
	if err != nil {
		return
	}

	clientsMu.RLock()
	defer clientsMu.RUnlock()

	for conn := range clients {
		conn.WriteMessage(websocket.TextMessage, data)
	}
}
