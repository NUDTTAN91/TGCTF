// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package question

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// HandleUploadAttachment 上传附件
func HandleUploadAttachment(c *gin.Context, db *sql.DB) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件"})
		return
	}
	defer file.Close()

	// 生成随机文件名
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	ext := filepath.Ext(header.Filename)
	newFilename := hex.EncodeToString(randBytes) + ext

	// 确保目录存在
	uploadDir := "./attachments"
	os.MkdirAll(uploadDir, 0755)

	// 保存文件
	dst, err := os.Create(filepath.Join(uploadDir, newFilename))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法保存文件"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
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

	filePath := filepath.Join("./attachments", filename)
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
