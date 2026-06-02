// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package question

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// maxAttachmentSize 附件体积上限。CTF 附件可能是镜像或流量包，上限给得宽松，
// 仅用于避免单次上传把磁盘写满。
const maxAttachmentSize = 512 << 20 // 512MB

// sanitizeExt 提取并清洗扩展名，只保留字母与数字。
// 该扩展名会进入落盘文件名和对外的下载 URL，不能直接采用上传方提供的内容。
func sanitizeExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return ""
	}

	var b strings.Builder
	b.WriteByte('.')
	for _, r := range ext[1:] {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}

	// 只剩下一个点，或长得不像扩展名，则不保留
	if b.Len() == 1 || b.Len() > 12 {
		return ""
	}
	return b.String()
}

// HandleUploadAttachment 上传附件
func HandleUploadAttachment(c *gin.Context, db *sql.DB) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件"})
		return
	}
	defer file.Close()

	// 先按声明的体积快速拒绝，避免整份大文件白写一遍
	if header.Size > maxAttachmentSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("附件不能超过 %d MB", maxAttachmentSize>>20),
		})
		return
	}

	// 生成随机文件名
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法生成文件名"})
		return
	}
	newFilename := hex.EncodeToString(randBytes) + sanitizeExt(header.Filename)

	// 确保目录存在
	uploadDir := "./attachments"
	os.MkdirAll(uploadDir, 0755)

	// 保存文件
	dstPath := filepath.Join(uploadDir, newFilename)
	dst, err := os.Create(dstPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法保存文件"})
		return
	}

	// header.Size 由客户端声明，实际写入仍需按上限截断兜底
	written, copyErr := io.Copy(dst, io.LimitReader(file, maxAttachmentSize+1))
	dst.Close()

	if copyErr != nil {
		os.Remove(dstPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
		return
	}
	if written > maxAttachmentSize {
		os.Remove(dstPath)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("附件不能超过 %d MB", maxAttachmentSize>>20),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"filename": newFilename,
		"url":      "/attachments/" + newFilename,
	})
}

// HandleDeleteAttachment 删除附件
func HandleDeleteAttachment(c *gin.Context, db *sql.DB) {
	filename := c.Param("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件名不能为空"})
		return
	}

	// 安全验证：拒绝包含路径分隔符或 .. 的文件名
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法文件名"})
		return
	}

	filePath := filepath.Join("./attachments", filename)

	// 二次验证：确保解析后的绝对路径仍在 attachments 目录内
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "路径解析失败"})
		return
	}
	baseDir, err := filepath.Abs("./attachments")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "路径解析失败"})
		return
	}
	if !strings.HasPrefix(absPath, baseDir+string(os.PathSeparator)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法文件名"})
		return
	}

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}
