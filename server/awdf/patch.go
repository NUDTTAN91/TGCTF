// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package awdf

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// AddPatchEventFunc 补丁事件记录函数（由 main.go 注入）
var AddPatchEventFunc func(db *sql.DB, contestID string, eventType string, teamName string, userName string, challengeName string)

// PatchRecord 补丁记录
type PatchRecord struct {
	ID           int64   `json:"id"`
	ContestID    int64   `json:"contestId"`
	ChallengeID  int64   `json:"challengeId"`
	TeamID       int64   `json:"teamId"`
	TeamName     string  `json:"teamName"`
	UserID       int64   `json:"userId"`
	Username     string  `json:"username"`
	PatchFile    string  `json:"patchFile"`
	PatchHash    *string `json:"patchHash"`
	Status       string  `json:"status"`
	RejectReason *string `json:"rejectReason"`
	AppliedAt    *string `json:"appliedAt"`
	CreatedAt    string  `json:"createdAt"`
}

// HandleUploadPatch 选手上传补丁
func HandleUploadPatch(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Param("challengeId")

	// 获取用户信息
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	// 获取用户队伍
	var teamID sql.NullInt64
	db.QueryRow("SELECT team_id FROM users WHERE id = $1", userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_TEAM", "message": "您还没有加入队伍"})
		return
	}

	// 验证比赛是否是AWD-F模式
	var contestMode string
	err := db.QueryRow("SELECT mode FROM contests WHERE id = $1", contestID).Scan(&contestMode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CONTEST_NOT_FOUND"})
		return
	}
	if contestMode != "awd-f" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AWDF_CONTEST", "message": "该比赛不是AWD-F模式"})
		return
	}

	// 验证题目存在且是AWD-F题目
	var questionID int64
	var patchWhitelist sql.NullString
	err = db.QueryRow(`
		SELECT cc.question_id, q.patch_whitelist 
		FROM contest_challenges_awdf cc
		JOIN question_bank_awdf q ON cc.question_id = q.id
		WHERE cc.id = $1 AND cc.contest_id = $2 AND cc.status = 'public'
	`, challengeID, contestID).Scan(&questionID, &patchWhitelist)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CHALLENGE_NOT_FOUND"})
		return
	}

	// 检查队伍是否已攻击成功（必须先解题才能上传补丁）
	var hasSolved bool
	err = db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM team_solves_awdf 
			WHERE team_id = $1 AND challenge_id = $2 AND contest_id = $3
		)
	`, teamID.Int64, challengeID, contestID).Scan(&hasSolved)
	if err != nil || !hasSolved {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "ATTACK_REQUIRED",
			"message": "必须先攻击成功（解题）才能上传补丁进行防守",
		})
		return
	}

	// 获取上传的文件
	file, header, err := c.Request.FormFile("patch")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_FILE", "message": "请上传补丁文件"})
		return
	}
	defer file.Close()

	// 验证文件后缀
	if !strings.HasSuffix(header.Filename, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_FILE_TYPE", "message": "补丁文件必须是.zip格式"})
		return
	}

	// 计算文件哈希
	hasher := sha256.New()
	tempFile, err := os.CreateTemp("", "patch-*.zip")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FILE_ERROR"})
		return
	}
	defer os.Remove(tempFile.Name())

	multiWriter := io.MultiWriter(tempFile, hasher)
	if _, err := io.Copy(multiWriter, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FILE_ERROR"})
		return
	}
	tempFile.Close()

	patchHash := hex.EncodeToString(hasher.Sum(nil))

	// 检查是否重复提交相同补丁
	var existingID int64
	err = db.QueryRow(`
		SELECT id FROM awdf_patches 
		WHERE contest_id = $1 AND challenge_id = $2 AND team_id = $3 AND patch_hash = $4
	`, contestID, challengeID, teamID.Int64, patchHash).Scan(&existingID)
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "DUPLICATE_PATCH", "message": "该补丁已提交过"})
		return
	}

	// 保存补丁文件
	patchDir := fmt.Sprintf("./patches/%s/%s/%d", contestID, challengeID, teamID.Int64)
	os.MkdirAll(patchDir, 0755)
	patchPath := filepath.Join(patchDir, fmt.Sprintf("%d_%s.zip", time.Now().Unix(), patchHash[:8]))

	destFile, err := os.Create(patchPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FILE_ERROR"})
		return
	}
	defer destFile.Close()

	srcFile, _ := os.Open(tempFile.Name())
	defer srcFile.Close()
	io.Copy(destFile, srcFile)

	// 插入补丁记录
	var patchID int64
	err = db.QueryRow(`
		INSERT INTO awdf_patches (contest_id, challenge_id, team_id, user_id, patch_file, patch_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		RETURNING id
	`, contestID, challengeID, teamID.Int64, userID, patchPath, patchHash).Scan(&patchID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR", "details": err.Error()})
		return
	}

	// 获取队伍名和题目名（用于事件记录）
	var teamName, challengeName string
	db.QueryRow(`SELECT name FROM teams WHERE id = $1`, teamID.Int64).Scan(&teamName)
	db.QueryRow(`SELECT q.title FROM contest_challenges_awdf cc JOIN question_bank_awdf q ON cc.question_id = q.id WHERE cc.id = $1`, challengeID).Scan(&challengeName)

	// 记录补丁提交事件
	if AddPatchEventFunc != nil {
		AddPatchEventFunc(db, contestID, "patch_submit", teamName, "", challengeName)
	}

	// 异步验证并应用补丁
	go applyPatch(db, patchID, patchPath, patchWhitelist.String, teamID.Int64, contestID, challengeID, teamName, challengeName)

	c.JSON(http.StatusOK, gin.H{
		"id":      patchID,
		"message": "补丁已提交，正在验证中",
		"status":  "pending",
	})
}

// applyPatch 验证并应用补丁到容器
func applyPatch(db *sql.DB, patchID int64, patchPath, whitelist string, teamID int64, contestID, challengeID string, teamName, challengeName string) {
	// 获取队伍的容器信息
	var containerID string
	err := db.QueryRow(`
		SELECT container_id FROM team_instances_awdf 
		WHERE team_id = $1 AND contest_id = $2 AND challenge_id = $3 AND status = 'running'
	`, teamID, contestID, challengeID).Scan(&containerID)

	if err != nil {
		updatePatchStatus(db, patchID, "failed", "未找到运行中的容器，请先部署环境")
		// 记录补丁失败事件
		if AddPatchEventFunc != nil {
			AddPatchEventFunc(db, contestID, "patch_rejected", teamName, "无容器", challengeName)
		}
		return
	}

	// 解压补丁到临时目录
	tempDir, err := os.MkdirTemp("", "patch-extract-*")
	if err != nil {
		updatePatchStatus(db, patchID, "failed", "解压失败")
		if AddPatchEventFunc != nil {
			AddPatchEventFunc(db, contestID, "patch_rejected", teamName, "解压失败", challengeName)
		}
		return
	}
	defer os.RemoveAll(tempDir)

	cmd := exec.Command("unzip", "-o", patchPath, "-d", tempDir)
	if err := cmd.Run(); err != nil {
		updatePatchStatus(db, patchID, "failed", "补丁文件解压失败，请确保是有效的ZIP文件")
		if AddPatchEventFunc != nil {
			AddPatchEventFunc(db, contestID, "patch_rejected", teamName, "ZIP无效", challengeName)
		}
		return
	}

	// 验证白名单
	var allowedFiles []string
	if whitelist != "" {
		if err := json.Unmarshal([]byte(whitelist), &allowedFiles); err != nil {
			allowedFiles = []string{}
		}
	}

	// 遍历解压的文件，检查是否在白名单内
	var filesToCopy []string
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, _ := filepath.Rel(tempDir, path)
		// 检查白名单
		if len(allowedFiles) > 0 {
			allowed := false
			for _, allowedPath := range allowedFiles {
				// 支持通配符匹配
				if strings.HasPrefix("/"+relPath, allowedPath) || relPath == strings.TrimPrefix(allowedPath, "/") {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("文件 %s 不在允许修改的白名单内", relPath)
			}
		}
		filesToCopy = append(filesToCopy, relPath)
		return nil
	})

	if err != nil {
		updatePatchStatus(db, patchID, "rejected", err.Error())
		if AddPatchEventFunc != nil {
			AddPatchEventFunc(db, contestID, "patch_rejected", teamName, "白名单", challengeName)
		}
		return
	}

	if len(filesToCopy) == 0 {
		updatePatchStatus(db, patchID, "rejected", "补丁包中没有有效文件")
		if AddPatchEventFunc != nil {
			AddPatchEventFunc(db, contestID, "patch_rejected", teamName, "无文件", challengeName)
		}
		return
	}

	// 复制文件到容器
	for _, relPath := range filesToCopy {
		srcPath := filepath.Join(tempDir, relPath)
		destPath := "/" + relPath
		cmd := exec.Command("docker", "cp", srcPath, containerID+":"+destPath)
		if err := cmd.Run(); err != nil {
			updatePatchStatus(db, patchID, "failed", fmt.Sprintf("应用补丁失败: %s", relPath))
			if AddPatchEventFunc != nil {
				AddPatchEventFunc(db, contestID, "patch_rejected", teamName, "应用失败", challengeName)
			}
			return
		}
	}

	// 更新状态为已应用
	updatePatchStatus(db, patchID, "applied", "")
	db.Exec("UPDATE awdf_patches SET applied_at = NOW() WHERE id = $1", patchID)

	// 记录补丁成功事件
	if AddPatchEventFunc != nil {
		AddPatchEventFunc(db, contestID, "patch_applied", teamName, "", challengeName)
	}
}

// updatePatchStatus 更新补丁状态
func updatePatchStatus(db *sql.DB, patchID int64, status, reason string) {
	if reason != "" {
		db.Exec("UPDATE awdf_patches SET status = $1, reject_reason = $2 WHERE id = $3", status, reason, patchID)
	} else {
		db.Exec("UPDATE awdf_patches SET status = $1 WHERE id = $2", status, patchID)
	}
}

// HandleGetPatchStatus 获取补丁状态
func HandleGetPatchStatus(c *gin.Context, db *sql.DB) {
	patchID := c.Param("patchId")

	var p PatchRecord
	var appliedAt sql.NullTime
	var rejectReason, patchHash sql.NullString
	var createdAt time.Time

	err := db.QueryRow(`
		SELECT p.id, p.contest_id, p.challenge_id, p.team_id, t.name, p.user_id, u.username,
			p.patch_file, p.patch_hash, p.status, p.reject_reason, p.applied_at, p.created_at
		FROM awdf_patches p
		JOIN teams t ON p.team_id = t.id
		JOIN users u ON p.user_id = u.id
		WHERE p.id = $1
	`, patchID).Scan(
		&p.ID, &p.ContestID, &p.ChallengeID, &p.TeamID, &p.TeamName, &p.UserID, &p.Username,
		&p.PatchFile, &patchHash, &p.Status, &rejectReason, &appliedAt, &createdAt,
	)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "PATCH_NOT_FOUND"})
		return
	}

	if patchHash.Valid {
		p.PatchHash = &patchHash.String
	}
	if rejectReason.Valid {
		p.RejectReason = &rejectReason.String
	}
	if appliedAt.Valid {
		t := appliedAt.Time.Format(time.RFC3339)
		p.AppliedAt = &t
	}
	p.CreatedAt = createdAt.Format(time.RFC3339)

	c.JSON(http.StatusOK, p)
}

// HandleListTeamPatches 获取队伍的补丁列表
func HandleListTeamPatches(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")
	challengeID := c.Param("challengeId")

	userID, _ := c.Get("userID")
	var teamID sql.NullInt64
	db.QueryRow("SELECT team_id FROM users WHERE id = $1", userID).Scan(&teamID)
	if !teamID.Valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "NO_TEAM"})
		return
	}

	rows, err := db.Query(`
		SELECT p.id, p.status, p.reject_reason, p.applied_at, p.created_at
		FROM awdf_patches p
		WHERE p.contest_id = $1 AND p.challenge_id = $2 AND p.team_id = $3
		ORDER BY p.created_at DESC
		LIMIT 10
	`, contestID, challengeID, teamID.Int64)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	type SimplePatch struct {
		ID           int64   `json:"id"`
		Status       string  `json:"status"`
		RejectReason *string `json:"rejectReason"`
		AppliedAt    *string `json:"appliedAt"`
		CreatedAt    string  `json:"createdAt"`
	}

	var patches []SimplePatch
	for rows.Next() {
		var p SimplePatch
		var rejectReason sql.NullString
		var appliedAt sql.NullTime
		var createdAt time.Time

		rows.Scan(&p.ID, &p.Status, &rejectReason, &appliedAt, &createdAt)
		if rejectReason.Valid {
			p.RejectReason = &rejectReason.String
		}
		if appliedAt.Valid {
			t := appliedAt.Time.Format(time.RFC3339)
			p.AppliedAt = &t
		}
		p.CreatedAt = createdAt.Format(time.RFC3339)
		patches = append(patches, p)
	}

	if patches == nil {
		patches = []SimplePatch{}
	}

	c.JSON(http.StatusOK, patches)
}

// HandleAdminListPatches 管理员获取所有补丁列表
func HandleAdminListPatches(c *gin.Context, db *sql.DB) {
	contestID := c.Param("id")

	rows, err := db.Query(`
		SELECT p.id, p.contest_id, p.challenge_id, p.team_id, t.name, p.user_id, u.username,
			p.patch_file, p.patch_hash, p.status, p.reject_reason, p.applied_at, p.created_at
		FROM awdf_patches p
		JOIN teams t ON p.team_id = t.id
		JOIN users u ON p.user_id = u.id
		WHERE p.contest_id = $1
		ORDER BY p.created_at DESC
	`, contestID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	var patches []PatchRecord
	for rows.Next() {
		var p PatchRecord
		var appliedAt sql.NullTime
		var rejectReason, patchHash sql.NullString
		var createdAt time.Time

		rows.Scan(
			&p.ID, &p.ContestID, &p.ChallengeID, &p.TeamID, &p.TeamName, &p.UserID, &p.Username,
			&p.PatchFile, &patchHash, &p.Status, &rejectReason, &appliedAt, &createdAt,
		)
		if patchHash.Valid {
			p.PatchHash = &patchHash.String
		}
		if rejectReason.Valid {
			p.RejectReason = &rejectReason.String
		}
		if appliedAt.Valid {
			t := appliedAt.Time.Format(time.RFC3339)
			p.AppliedAt = &t
		}
		p.CreatedAt = createdAt.Format(time.RFC3339)
		patches = append(patches, p)
	}

	if patches == nil {
		patches = []PatchRecord{}
	}

	c.JSON(http.StatusOK, patches)
}
