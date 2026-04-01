// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package contest

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"tgctf/server/monitor"
)

// Announcement 公告结构
type Announcement struct {
	ID        int64  `json:"id"`
	ContestID int64  `json:"contestId"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	IsPinned  bool   `json:"isPinned"`
	CreatedBy *int64 `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// CreateAnnouncementRequest 创建公告请求
type CreateAnnouncementRequest struct {
	Title    string `json:"title" binding:"required"`
	Content  string `json:"content"`
	IsPinned bool   `json:"isPinned"`
}

// HandleListAnnouncements 获取比赛公告列表（公开API）
func HandleListAnnouncements(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	rows, err := db.Query(`
		SELECT id, contest_id, type, title, COALESCE(content, ''), is_pinned, created_by, created_at
		FROM contest_announcements 
		WHERE contest_id = $1 
		ORDER BY is_pinned DESC, created_at DESC`, contestID)
	if err != nil {
		log.Printf("query announcements error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	var announcements []Announcement
	for rows.Next() {
		var a Announcement
		var createdAt time.Time
		var createdBy sql.NullInt64
		if err := rows.Scan(&a.ID, &a.ContestID, &a.Type, &a.Title, &a.Content, &a.IsPinned, &createdBy, &createdAt); err != nil {
			continue
		}
		if createdBy.Valid {
			a.CreatedBy = &createdBy.Int64
		}
		a.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		announcements = append(announcements, a)
	}

	if announcements == nil {
		announcements = []Announcement{}
	}

	c.JSON(http.StatusOK, announcements)
}

// HandleCreateAnnouncement 管理员手动创建公告
func HandleCreateAnnouncement(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	userID := c.GetInt64("userID")

	var req CreateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查 userID 是否在 users 表中存在
	var createdBy interface{}
	if userID > 0 {
		var exists bool
		db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists)
		if exists {
			createdBy = userID
		} else {
			createdBy = nil
		}
	} else {
		createdBy = nil
	}

	var id int64
	err := db.QueryRow(`
		INSERT INTO contest_announcements (contest_id, type, title, content, is_pinned, created_by)
		VALUES ($1, 'manual', $2, $3, $4, $5) RETURNING id`,
		contestID, req.Title, req.Content, req.IsPinned, createdBy).Scan(&id)

	if err != nil {
		log.Printf("create announcement error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}

	createdAt := time.Now().Format("2006-01-02 15:04:05")
	go monitor.BroadcastMonitorUpdate(contestID, map[string]interface{}{
		"type":   "announcement_create",
		"announcement": map[string]interface{}{"id": id, "type": "manual", "title": req.Title, "content": req.Content, "isPinned": req.IsPinned, "createdAt": createdAt},
	})

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "CREATED"})
}

// HandleUpdateAnnouncement 更新公告
func HandleUpdateAnnouncement(c *gin.Context, db *sql.DB) {
	announcementID := c.Param("announcementId")

	var req struct {
		Title    string `json:"title"`
		Content  string `json:"content"`
		IsPinned *bool  `json:"isPinned"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 只允许修改手动公告
	var annType string
	err := db.QueryRow(`SELECT type FROM contest_announcements WHERE id = $1`, announcementID).Scan(&annType)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if annType != "manual" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CANNOT_EDIT_AUTO_ANNOUNCEMENT", "message": "只能编辑手动发布的公告"})
		return
	}

	// 构建更新
	if req.Title != "" {
		db.Exec(`UPDATE contest_announcements SET title = $1 WHERE id = $2`, req.Title, announcementID)
	}
	if req.Content != "" {
		db.Exec(`UPDATE contest_announcements SET content = $1 WHERE id = $2`, req.Content, announcementID)
	}
	if req.IsPinned != nil {
		db.Exec(`UPDATE contest_announcements SET is_pinned = $1 WHERE id = $2`, *req.IsPinned, announcementID)
	}

	// WebSocket 广播公告更新
	contestID := ""
	db.QueryRow(`SELECT contest_id FROM contest_announcements WHERE id = $1`, announcementID).Scan(&contestID)
	if contestID != "" {
		go func() {
			var a Announcement
			var cat time.Time
			db.QueryRow(`SELECT id, contest_id, type, title, COALESCE(content,''), is_pinned, created_at FROM contest_announcements WHERE id = $1`, announcementID).Scan(&a.ID, &a.ContestID, &a.Type, &a.Title, &a.Content, &a.IsPinned, &cat)
			a.CreatedAt = cat.Format("2006-01-02 15:04:05")
			monitor.BroadcastMonitorUpdate(contestID, map[string]interface{}{
				"type":         "announcement_update",
				"announcement": a,
			})
		}()
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED"})
}

// HandleDeleteAnnouncement 删除公告
func HandleDeleteAnnouncement(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	announcementID := c.Param("announcementId")

	result, err := db.Exec(`DELETE FROM contest_announcements WHERE id = $1`, announcementID)
	if err != nil {
		log.Printf("delete announcement error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	// WebSocket 广播公告删除
	go monitor.BroadcastMonitorUpdate(contestID, map[string]interface{}{
		"type":           "announcement_delete",
		"announcementId": announcementID,
	})

	c.JSON(http.StatusOK, gin.H{"message": "DELETED"})
}

// ========== 自动公告函数 ==========

// CreateAutoAnnouncement 创建自动公告
func CreateAutoAnnouncement(db *sql.DB, contestID int64, annType, title, content string) error {
	_, err := db.Exec(`
		INSERT INTO contest_announcements (contest_id, type, title, content)
		VALUES ($1, $2, $3, $4)`,
		contestID, annType, title, content)
	if err != nil {
		log.Printf("create auto announcement error: %v", err)
	} else {
		createdAt := time.Now().Format("2006-01-02 15:04:05")
		go monitor.BroadcastMonitorUpdate(strconv.FormatInt(contestID, 10), map[string]interface{}{
			"type":         "announcement_create",
			"announcement": map[string]interface{}{"type": annType, "title": title, "content": content, "isPinned": false, "createdAt": createdAt},
		})
	}
	return err
}

// AnnounceChallenge 题目开放/下架公告
func AnnounceChallenge(db *sql.DB, contestID int64, challengeName, action string) {
	var annType, title, content string
	if action == "open" {
		annType = "challenge_open"
		title = fmt.Sprintf("📢 题目开放: %s", challengeName)
		content = fmt.Sprintf("题目【%s】已开放，快来挑战吧！", challengeName)
	} else if action == "hint" {
		annType = "challenge_hint"
		title = fmt.Sprintf("💡 题目提示: %s", challengeName)
		content = fmt.Sprintf("题目【%s】已放出提示，快去查看吧！", challengeName)
	} else {
		annType = "challenge_close"
		title = fmt.Sprintf("📢 题目下架: %s", challengeName)
		content = fmt.Sprintf("题目【%s】已下架。", challengeName)
	}
	CreateAutoAnnouncement(db, contestID, annType, title, content)
}

// AnnounceBlood 一二三血公告
func AnnounceBlood(db *sql.DB, contestID int64, challengeName, teamName string, bloodType int) {
	var annType, emoji, bloodName string
	switch bloodType {
	case 1:
		annType = "first_blood"
		emoji = "🥇"
		bloodName = "一血"
	case 2:
		annType = "second_blood"
		emoji = "🥈"
		bloodName = "二血"
	case 3:
		annType = "third_blood"
		emoji = "🥉"
		bloodName = "三血"
	default:
		return
	}
	title := fmt.Sprintf("%s %s: %s", emoji, bloodName, challengeName)
	content := fmt.Sprintf("恭喜【%s】获得题目【%s】的%s！", teamName, challengeName, bloodName)
	CreateAutoAnnouncement(db, contestID, annType, title, content)
}

// AnnounceCheating 作弊封禁公告
func AnnounceCheating(db *sql.DB, contestID int64, teamName, reason string) {
	title := fmt.Sprintf("⚠️ 违规处罚: %s", teamName)
	content := fmt.Sprintf("队伍【%s】因违规行为已被封禁。", teamName)
	if reason != "" {
		content += fmt.Sprintf("原因: %s", reason)
	}
	CreateAutoAnnouncement(db, contestID, "cheating", title, content)
}
