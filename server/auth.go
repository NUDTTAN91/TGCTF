// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"tgctf/server/admin"
	"tgctf/server/logs"
)

// ensureAdmin 确保超级管理员账户、TG_AdminX组织和队伍存在
func ensureAdmin(db *sql.DB) error {
	username := os.Getenv("ADMIN_USERNAME")
	password := os.Getenv("ADMIN_PASSWORD")
	displayName := os.Getenv("ADMIN_DISPLAY_NAME")

	if username == "" || password == "" {
		return nil
	}

	if displayName == "" {
		displayName = username
	}

	// 超管默认头像
	hackerAvatar := "/assets/hacker.png"

	// 1. 确保 TG_AdminX 组织存在（超管组织，拥有所有比赛查看权限）
	var orgID int64
	err := db.QueryRow(`SELECT id FROM organizations WHERE name = 'TG_AdminX'`).Scan(&orgID)
	if err == sql.ErrNoRows {
		err = db.QueryRow(`INSERT INTO organizations (name, description, status, created_at, updated_at) 
			VALUES ('TG_AdminX', '超级管理员专属组织，拥有所有比赛查看权限', 'active', NOW(), NOW()) RETURNING id`).Scan(&orgID)
		if err != nil {
			return err
		}
		log.Printf("[ensureAdmin] Created TG_AdminX organization with ID: %d", orgID)
	} else if err != nil {
		return err
	}

	// 2. 确保 TG_AdminX 队伍存在（关联到TG_AdminX组织，使用hacker.png作为默认头像）
	var teamID int64
	err = db.QueryRow(`SELECT id FROM teams WHERE name = 'TG_AdminX'`).Scan(&teamID)
	if err == sql.ErrNoRows {
		err = db.QueryRow(`INSERT INTO teams (name, description, organization_id, is_admin_team, avatar, status, created_at, updated_at) 
			VALUES ('TG_AdminX', '超级管理员专属队伍', $1, TRUE, $2, 'active', NOW(), NOW()) RETURNING id`, orgID, hackerAvatar).Scan(&teamID)
		if err != nil {
			return err
		}
		log.Printf("[ensureAdmin] Created TG_AdminX team with ID: %d", teamID)
	} else if err != nil {
		return err
	} else {
		// 确保队伍关联到组织，并设置默认头像
		db.Exec(`UPDATE teams SET organization_id = $1, avatar = COALESCE(avatar, $2) WHERE id = $3`, orgID, hackerAvatar, teamID)
	}

	// 3. 生成密码哈希
	hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if hashErr != nil {
		return hashErr
	}

	// 4. 删除所有其他超管（确保只有一个超管，由docker-compose.yml完全控制）
	// 先清理外键引用
	var otherSuperIDs []int64
	otherRows, _ := db.Query(`SELECT id FROM users WHERE role = 'super' AND username != $1`, username)
	if otherRows != nil {
		for otherRows.Next() {
			var uid int64
			otherRows.Scan(&uid)
			otherSuperIDs = append(otherSuperIDs, uid)
		}
		otherRows.Close()
	}
	for _, uid := range otherSuperIDs {
		db.Exec(`UPDATE team_instances SET created_by = NULL WHERE created_by = $1`, uid)
		db.Exec(`UPDATE system_logs SET user_id = NULL WHERE user_id = $1`, uid)
		db.Exec(`UPDATE contest_teams SET reviewed_by = NULL WHERE reviewed_by = $1`, uid)
		db.Exec(`UPDATE teams SET captain_id = NULL WHERE captain_id = $1`, uid)
		db.Exec(`DELETE FROM user_login_history WHERE user_id = $1`, uid)
		db.Exec(`DELETE FROM challenge_first_views WHERE user_id = $1`, uid)
		db.Exec(`DELETE FROM team_solves WHERE first_solver_id = $1`, uid)
		db.Exec(`DELETE FROM users WHERE id = $1`, uid)
		log.Printf("[ensureAdmin] Deleted redundant super admin (ID: %d)", uid)
	}

	// 5. 按 username 查找用户
	var existingID int64
	err = db.QueryRow(`SELECT id FROM users WHERE username = $1`, username).Scan(&existingID)

	if err == sql.ErrNoRows {
		// 用户不存在，创建新超管
		var newID int64
		err = db.QueryRow(`INSERT INTO users (username, display_name, role, password_hash, team_id, organization_id, avatar, status, created_at, updated_at) 
			VALUES ($1, $2, 'super', $3, $4, $5, $6, 'active', NOW(), NOW()) RETURNING id`,
			username, displayName, string(hash), teamID, orgID, hackerAvatar).Scan(&newID)
		if err != nil {
			return err
		}
		db.Exec(`UPDATE teams SET captain_id = $1 WHERE id = $2`, newID, teamID)
		log.Printf("[ensureAdmin] Created super admin: %s (ID: %d)", username, newID)
	} else if err == nil {
		// 用户已存在，更新为超管并更新密码
		_, err = db.Exec(`UPDATE users SET role = 'super', display_name = $1, password_hash = $2, team_id = $3, organization_id = $4, avatar = COALESCE(avatar, $5), status = 'active', updated_at = NOW() WHERE id = $6`,
			displayName, string(hash), teamID, orgID, hackerAvatar, existingID)
		if err != nil {
			return err
		}
		db.Exec(`UPDATE teams SET captain_id = $1 WHERE id = $2`, existingID, teamID)
		log.Printf("[ensureAdmin] Updated super admin: %s (ID: %d)", username, existingID)
	} else {
		return err
	}

	return nil
}

// handleLogin 处理登录请求
func handleLogin(c *gin.Context, db *sql.DB, secret []byte) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var (
		id                 int64
		username           string
		displayName        string
		role               string
		passwordHash       string
		mustChangePassword bool
		tokenVersion       int
		status             string
	)

	err := db.QueryRow(
		`SELECT id, username, display_name, role, password_hash, COALESCE(must_change_password, FALSE), COALESCE(token_version, 1), COALESCE(status, 'active') FROM users WHERE username = $1`,
		req.Username,
	).Scan(&id, &username, &displayName, &role, &passwordHash, &mustChangePassword, &tokenVersion, &status)

	clientIP := c.ClientIP()

	if err == sql.ErrNoRows {
		// 用户不存在，记录失败日志
		logs.WriteLog(db, logs.TypeLogin, logs.LevelError, nil, nil, nil, nil, clientIP,
			"登录失败: 用户 ["+req.Username+"] 不存在", nil)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CREDENTIALS"})
		return
	}
	if err != nil {
		log.Printf("query user error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 检查用户是否被封禁
	if status == "banned" {
		logs.WriteLog(db, logs.TypeLogin, logs.LevelError, &id, nil, nil, nil, clientIP,
			"登录失败: 用户 ["+displayName+"] 已被封禁", nil)
		c.JSON(http.StatusForbidden, gin.H{"error": "ACCOUNT_DISABLED", "message": "该账号不可用，请联系管理员"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		// 密码错误，记录失败日志
		logs.WriteLog(db, logs.TypeLogin, logs.LevelError, &id, nil, nil, nil, clientIP,
			"登录失败: 用户 ["+displayName+"] 密码错误", nil)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CREDENTIALS"})
		return
	}

	// 更新最后登录IP和时间
	db.Exec(`UPDATE users SET last_login_ip = $1, last_login_at = NOW(), updated_at = NOW() WHERE id = $2`, clientIP, id)

	// 记录登录历史（用于IP异常检测）
	userAgent := c.GetHeader("User-Agent")
	db.Exec(`INSERT INTO user_login_history (user_id, ip_address, user_agent, login_at) VALUES ($1, $2, $3, NOW())`,
		id, clientIP, userAgent)

	// 检测登录IP变化并推送WebSocket通知（仅普通用户）
	if role == "user" {
		var prevIP sql.NullString
		db.QueryRow(`
			SELECT ip_address FROM user_login_history 
			WHERE user_id = $1 AND login_at < NOW() 
			ORDER BY login_at DESC LIMIT 1 OFFSET 1
		`, id).Scan(&prevIP)
		
		if prevIP.Valid && prevIP.String != clientIP {
			go admin.BroadcastLoginIPChange(db, id, displayName, prevIP.String, clientIP)
		}
	}

	// 记录登录日志
	logs.WriteLogSimple(db, logs.TypeLogin, logs.LevelSuccess, id, clientIP, displayName+" 登录系统")

	token, err := generateJWT(User{
		ID:          id,
		Username:    username,
		DisplayName: displayName,
		Role:        role,
	}, secret, tokenVersion)
	if err != nil {
		log.Printf("generate token error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": User{
			ID:          id,
			Username:    username,
			DisplayName: displayName,
			Role:        role,
		},
		"mustChangePassword": mustChangePassword,
	})
}

// generateJWT 生成JWT令牌
func generateJWT(u User, secret []byte, tokenVersion int) (string, error) {
	claims := jwt.MapClaims{
		"sub":          u.ID,
		"username":     u.Username,
		"displayName":  u.DisplayName,
		"role":         u.Role,
		"tokenVersion": tokenVersion,
		"exp":          time.Now().Add(24 * time.Hour).Unix(),
		"iat":          time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}
