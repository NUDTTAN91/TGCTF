// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package user

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tgctf/server/logs"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// ProfileInfo 用户个人信息
type ProfileInfo struct {
	ID               int64   `json:"id"`
	Username         string  `json:"username"`
	DisplayName      string  `json:"displayName"`
	Email            *string `json:"email"`
	Role             string  `json:"role"`
	Avatar           *string `json:"avatar"`
	TeamID           *int64  `json:"teamId"`
	TeamName         *string `json:"teamName"`
	OrganizationID   *int64  `json:"organizationId"`
	OrganizationName *string `json:"organizationName"`
	LastLoginIP      *string `json:"lastLoginIp"`
	LastLoginAt      *string `json:"lastLoginAt"`
	CreatedAt        string  `json:"createdAt"`
}

// TeamInfo 队伍信息
type TeamInfo struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Avatar      *string      `json:"avatar"`    // 队伍头像（如为空则使用队长头像）
	CaptainID   *int64       `json:"captainId"`
	CaptainName string       `json:"captainName"`
	IsCaptain   bool         `json:"isCaptain"` // 当前用户是否为队长
	Members     []TeamMember `json:"members"`
	CreatedAt   string       `json:"createdAt"`
}

// TeamMember 队伍成员
type TeamMember struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	IsCaptain   bool   `json:"isCaptain"`
}

// UpdateProfileRequest 更新个人信息请求
type UpdateProfileRequest struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword"` // 强制改密时可为空
	NewPassword string `json:"newPassword" binding:"required"`
}

// ValidatePasswordStrength 验证密码强度：必须包含大小写字母、数字、特殊符号
func ValidatePasswordStrength(password string) (bool, string) {
	if len(password) < 8 {
		return false, "密码长度至少8位"
	}
	// 大写字母
	hasUpper := regexp.MustCompile(`[A-Z]`).MatchString(password)
	if !hasUpper {
		return false, "密码必须包含大写字母"
	}
	// 小写字母
	hasLower := regexp.MustCompile(`[a-z]`).MatchString(password)
	if !hasLower {
		return false, "密码必须包含小写字母"
	}
	// 数字
	hasDigit := regexp.MustCompile(`[0-9]`).MatchString(password)
	if !hasDigit {
		return false, "密码必须包含数字"
	}
	// 特殊符号
	hasSpecial := regexp.MustCompile(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>/?~]`).MatchString(password)
	if !hasSpecial {
		return false, "密码必须包含特殊符号(!@#$%^&*等)"
	}
	return true, ""
}

// HandleGetProfile 获取当前用户个人信息
func HandleGetProfile(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")

	var p ProfileInfo
	var email, avatar, teamName, orgName, lastLoginIP, lastLoginAt sql.NullString
	var teamID, orgID sql.NullInt64

	err := db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.avatar,
		       u.team_id, t.name,
		       u.organization_id, o.name,
		       u.last_login_ip,
		       COALESCE(TO_CHAR(u.last_login_at, 'YYYY-MM-DD HH24:MI'), ''),
		       COALESCE(TO_CHAR(u.created_at, 'YYYY-MM-DD HH24:MI'), '')
		FROM users u
		LEFT JOIN teams t ON u.team_id = t.id
		LEFT JOIN organizations o ON u.organization_id = o.id
		WHERE u.id = $1`, userID).Scan(
		&p.ID, &p.Username, &p.DisplayName, &email, &p.Role, &avatar,
		&teamID, &teamName,
		&orgID, &orgName,
		&lastLoginIP, &lastLoginAt, &p.CreatedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "USER_NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("get profile error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	if email.Valid {
		p.Email = &email.String
	}
	if avatar.Valid {
		p.Avatar = &avatar.String
	}
	if teamID.Valid {
		p.TeamID = &teamID.Int64
	}
	if teamName.Valid {
		p.TeamName = &teamName.String
	}
	if orgID.Valid {
		p.OrganizationID = &orgID.Int64
	}
	if orgName.Valid {
		p.OrganizationName = &orgName.String
	}
	if lastLoginIP.Valid {
		p.LastLoginIP = &lastLoginIP.String
	}
	if lastLoginAt.Valid && lastLoginAt.String != "" {
		p.LastLoginAt = &lastLoginAt.String
	}

	c.JSON(http.StatusOK, p)
}

// HandleUpdateProfile 更新个人信息
func HandleUpdateProfile(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 构建更新语句
	updates := ""
	args := []interface{}{}
	argIdx := 1

	if req.DisplayName != "" {
		updates += "display_name = $" + string(rune('0'+argIdx)) + ", "
		args = append(args, req.DisplayName)
		argIdx++
	}

	if req.Email != "" {
		updates += "email = $" + string(rune('0'+argIdx)) + ", "
		args = append(args, req.Email)
		argIdx++
	}

	if len(args) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates += "updated_at = NOW()"
	args = append(args, userID)

	query := "UPDATE users SET " + updates + " WHERE id = $" + string(rune('0'+argIdx))
	_, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("update profile error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED"})
}

// HandleChangePassword 修改密码
func HandleChangePassword(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 验证密码强度
	if valid, msg := ValidatePasswordStrength(req.NewPassword); !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WEAK_PASSWORD", "message": msg})
		return
	}

	// 检查是否是强制改密状态
	var mustChangePassword bool
	var currentHash string
	err := db.QueryRow(`SELECT password_hash, COALESCE(must_change_password, FALSE) FROM users WHERE id = $1`, userID).Scan(&currentHash, &mustChangePassword)
	if err != nil {
		log.Printf("get password hash error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 如果不是强制改密状态，则需要验证旧密码
	if !mustChangePassword {
		if req.OldPassword == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "OLD_PASSWORD_REQUIRED"})
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.OldPassword)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "WRONG_PASSWORD"})
			return
		}
	}

	// 生成新密码哈希
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("generate password hash error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 更新密码并清除 must_change_password 标记
	_, err = db.Exec(`UPDATE users SET password_hash = $1, must_change_password = FALSE, updated_at = NOW() WHERE id = $2`, string(newHash), userID)
	if err != nil {
		log.Printf("update password error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 记录修改密码日志
	clientIP := c.ClientIP()
	logs.WriteLogSimple(db, logs.TypePasswordChange, logs.LevelInfo, userID, clientIP, "用户修改密码")

	c.JSON(http.StatusOK, gin.H{"message": "PASSWORD_CHANGED"})
}

// HandleGetMyTeam 获取当前用户的队伍信息
func HandleGetMyTeam(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")

	// 先获取用户的队伍ID
	var teamID sql.NullInt64
	err := db.QueryRow(`SELECT team_id FROM users WHERE id = $1`, userID).Scan(&teamID)
	if err != nil {
		log.Printf("get user team_id error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	if !teamID.Valid {
		c.JSON(http.StatusOK, gin.H{"team": nil, "message": "NO_TEAM"})
		return
	}

	// 获取队伍信息（包含队伍头像和队长头像）
	var team TeamInfo
	var desc, captainName, teamAvatar, captainAvatar sql.NullString
	var captainID sql.NullInt64

	err = db.QueryRow(`
		SELECT t.id, t.name, COALESCE(t.description, ''), t.avatar, t.captain_id, 
		       COALESCE(u.display_name, ''), u.avatar,
		       COALESCE(TO_CHAR(t.created_at, 'YYYY-MM-DD HH24:MI'), '')
		FROM teams t
		LEFT JOIN users u ON t.captain_id = u.id
		WHERE t.id = $1`, teamID.Int64).Scan(
		&team.ID, &team.Name, &desc, &teamAvatar, &captainID, &captainName, &captainAvatar, &team.CreatedAt)
	if err != nil {
		log.Printf("get team info error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	team.Description = desc.String
	// 处理头像：队伍头像优先，如果为空则使用队长头像
	if teamAvatar.Valid && teamAvatar.String != "" {
		team.Avatar = &teamAvatar.String
	} else if captainAvatar.Valid && captainAvatar.String != "" {
		team.Avatar = &captainAvatar.String
	}
	if captainID.Valid {
		team.CaptainID = &captainID.Int64
		team.IsCaptain = captainID.Int64 == userID
	}
	team.CaptainName = captainName.String

	// 获取队伍成员
	rows, err := db.Query(`
		SELECT id, username, display_name
		FROM users 
		WHERE team_id = $1
		ORDER BY id ASC`, teamID.Int64)
	if err != nil {
		log.Printf("get team members error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	team.Members = []TeamMember{}
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.ID, &m.Username, &m.DisplayName); err != nil {
			continue
		}
		m.IsCaptain = captainID.Valid && m.ID == captainID.Int64
		team.Members = append(team.Members, m)
	}

	c.JSON(http.StatusOK, gin.H{"team": team})
}

// HandleUploadAvatar 上传头像
func HandleUploadAvatar(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")

	// 支持两种上传方式：form-data 或 base64 JSON
	contentType := c.GetHeader("Content-Type")

	var avatarPath string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// 文件上传方式
		file, header, err := c.Request.FormFile("avatar")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "NO_FILE"})
			return
		}
		defer file.Close()

		// 验证文件类型
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_FILE_TYPE"})
			return
		}

		// 验证文件大小（最大 2MB）
		if header.Size > 2*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_TOO_LARGE"})
			return
		}

		// 创建头像目录
		avatarDir := "web/uploads/avatars"
		if err := os.MkdirAll(avatarDir, 0755); err != nil {
			log.Printf("create avatar dir error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		// 生成文件名
		filename := fmt.Sprintf("%d_%d%s", userID, time.Now().UnixNano(), ext)
		filePath := filepath.Join(avatarDir, filename)

		// 保存文件
		out, err := os.Create(filePath)
		if err != nil {
			log.Printf("create file error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			log.Printf("save file error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		avatarPath = "/uploads/avatars/" + filename

	} else {
		// Base64 上传方式
		var req struct {
			Avatar string `json:"avatar"` // data:image/png;base64,xxxxx
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
			return
		}

		if req.Avatar == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "NO_AVATAR"})
			return
		}

		// 解析 base64 数据
		var ext, data string
		if strings.HasPrefix(req.Avatar, "data:image/") {
			parts := strings.SplitN(req.Avatar, ",", 2)
			if len(parts) != 2 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_BASE64"})
				return
			}
			// 获取图片类型
			if strings.Contains(parts[0], "png") {
				ext = ".png"
			} else if strings.Contains(parts[0], "gif") {
				ext = ".gif"
			} else if strings.Contains(parts[0], "webp") {
				ext = ".webp"
			} else {
				ext = ".jpg"
			}
			data = parts[1]
		} else {
			ext = ".jpg"
			data = req.Avatar
		}

		// 解码
		imgData, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_BASE64"})
			return
		}

		// 验证大小
		if len(imgData) > 2*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_TOO_LARGE"})
			return
		}

		// 创建头像目录
		avatarDir := "web/uploads/avatars"
		if err := os.MkdirAll(avatarDir, 0755); err != nil {
			log.Printf("create avatar dir error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		// 生成文件名
		filename := fmt.Sprintf("%d_%d%s", userID, time.Now().UnixNano(), ext)
		filePath := filepath.Join(avatarDir, filename)

		// 保存文件
		if err := os.WriteFile(filePath, imgData, 0644); err != nil {
			log.Printf("save file error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		avatarPath = "/uploads/avatars/" + filename
	}

	// 删除旧头像
	var oldAvatar sql.NullString
	db.QueryRow(`SELECT avatar FROM users WHERE id = $1`, userID).Scan(&oldAvatar)
	if oldAvatar.Valid && oldAvatar.String != "" && strings.HasPrefix(oldAvatar.String, "/uploads/avatars/") {
		oldPath := "web" + oldAvatar.String
		os.Remove(oldPath)
	}

	// 更新数据库
	_, err := db.Exec(`UPDATE users SET avatar = $1, updated_at = NOW() WHERE id = $2`, avatarPath, userID)
	if err != nil {
		log.Printf("update avatar error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 记录更新个人头像日志
	clientIP := c.ClientIP()
	var displayName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName)
	logs.WriteLogSimple(db, logs.TypeAvatarUpdate, logs.LevelInfo, userID, clientIP, displayName+" 更新了个人头像")

	c.JSON(http.StatusOK, gin.H{"avatar": avatarPath})
}

// HandleUploadTeamAvatar 上传队伍头像（仅队长可以修改）
func HandleUploadTeamAvatar(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")

	// 获取用户的队伍信息
	var teamID sql.NullInt64
	var captainID sql.NullInt64
	err := db.QueryRow(`
		SELECT u.team_id, t.captain_id 
		FROM users u 
		LEFT JOIN teams t ON u.team_id = t.id 
		WHERE u.id = $1`, userID).Scan(&teamID, &captainID)
	if err != nil {
		log.Printf("get team info error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	if !teamID.Valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_TEAM"})
		return
	}

	// 检查是否是队长
	if !captainID.Valid || captainID.Int64 != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "NOT_CAPTAIN"})
		return
	}

	// 支持两种上传方式：form-data 或 base64 JSON
	contentType := c.GetHeader("Content-Type")

	var avatarPath string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// 文件上传方式
		file, header, err := c.Request.FormFile("avatar")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "NO_FILE"})
			return
		}
		defer file.Close()

		// 验证文件类型
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_FILE_TYPE"})
			return
		}

		// 验证文件大小（最大 2MB）
		if header.Size > 2*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_TOO_LARGE"})
			return
		}

		// 创建队伍头像目录
		avatarDir := "web/uploads/team-avatars"
		if err := os.MkdirAll(avatarDir, 0755); err != nil {
			log.Printf("create team avatar dir error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		// 生成文件名
		filename := fmt.Sprintf("team_%d_%d%s", teamID.Int64, time.Now().UnixNano(), ext)
		filePath := filepath.Join(avatarDir, filename)

		// 保存文件
		out, err := os.Create(filePath)
		if err != nil {
			log.Printf("create file error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			log.Printf("save file error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		avatarPath = "/uploads/team-avatars/" + filename

	} else {
		// Base64 上传方式
		var req struct {
			Avatar string `json:"avatar"` // data:image/png;base64,xxxxx
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
			return
		}

		if req.Avatar == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "NO_AVATAR"})
			return
		}

		// 解析 base64 数据
		var ext, data string
		if strings.HasPrefix(req.Avatar, "data:image/") {
			parts := strings.SplitN(req.Avatar, ",", 2)
			if len(parts) != 2 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_BASE64"})
				return
			}
			if strings.Contains(parts[0], "png") {
				ext = ".png"
			} else if strings.Contains(parts[0], "gif") {
				ext = ".gif"
			} else if strings.Contains(parts[0], "webp") {
				ext = ".webp"
			} else {
				ext = ".jpg"
			}
			data = parts[1]
		} else {
			ext = ".jpg"
			data = req.Avatar
		}

		// 解码
		imgData, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_BASE64"})
			return
		}

		// 验证大小
		if len(imgData) > 2*1024*1024 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_TOO_LARGE"})
			return
		}

		// 创建队伍头像目录
		avatarDir := "web/uploads/team-avatars"
		if err := os.MkdirAll(avatarDir, 0755); err != nil {
			log.Printf("create team avatar dir error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		// 生成文件名
		filename := fmt.Sprintf("team_%d_%d%s", teamID.Int64, time.Now().UnixNano(), ext)
		filePath := filepath.Join(avatarDir, filename)

		// 保存文件
		if err := os.WriteFile(filePath, imgData, 0644); err != nil {
			log.Printf("save file error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}

		avatarPath = "/uploads/team-avatars/" + filename
	}

	// 删除旧头像
	var oldAvatar sql.NullString
	db.QueryRow(`SELECT avatar FROM teams WHERE id = $1`, teamID.Int64).Scan(&oldAvatar)
	if oldAvatar.Valid && oldAvatar.String != "" && strings.HasPrefix(oldAvatar.String, "/uploads/team-avatars/") {
		oldPath := "web" + oldAvatar.String
		os.Remove(oldPath)
	}

	// 更新数据库
	_, err = db.Exec(`UPDATE teams SET avatar = $1, updated_at = NOW() WHERE id = $2`, avatarPath, teamID.Int64)
	if err != nil {
		log.Printf("update team avatar error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 记录更新队伍头像日志
	clientIP := c.ClientIP()
	var displayName, teamName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName)
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
	logs.WriteLog(db, logs.TypeAvatarUpdate, logs.LevelInfo, &userID, &teamID.Int64, nil, nil, clientIP,
		displayName+" 更新了队伍 ["+teamName+"] 的头像", nil)

	c.JSON(http.StatusOK, gin.H{"avatar": avatarPath})
}

// HandleLogout 用户退出登录
func HandleLogout(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	clientIP := c.ClientIP()

	// 获取用户名称
	var displayName string
	db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&displayName)

	// 记录退出日志
	logs.WriteLogSimple(db, logs.TypeLogout, logs.LevelInfo, userID, clientIP, displayName+" 退出系统")

	c.JSON(http.StatusOK, gin.H{"message": "LOGGED_OUT"})
}
