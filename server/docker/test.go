// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package docker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// DockerTestRequest Docker测试请求
type DockerTestRequest struct {
	Image string `json:"image" binding:"required"`
}

// DockerCreateContainerRequest 创建测试容器请求
type DockerCreateContainerRequest struct {
	QuestionID  int64    `json:"questionId"`
	Image       string   `json:"image" binding:"required"`
	Ports       []string `json:"ports"`
	CPULimit    string   `json:"cpuLimit"`
	MemoryLimit string   `json:"memoryLimit"`
	Flag        string   `json:"flag"`
	FlagEnvs    []string `json:"flagEnvs"`
	FlagScript  string   `json:"flagScript"`
}

// TestContainer 测试容器信息
type TestContainer struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	QuestionID int64             `json:"questionId"`
	Image      string            `json:"image"`
	Status     string            `json:"status"`
	Ports      map[string]string `json:"ports"`
	CreatedAt  string            `json:"createdAt"`
}

// HandleCheckDockerImage 检查Docker镜像是否存在
func HandleCheckDockerImage(c *gin.Context, db *sql.DB) {
	var req DockerTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "details": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--type=image", req.Image)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if strings.Contains(string(output), "No such image") || strings.Contains(string(output), "Error: No such object") {
			c.JSON(http.StatusOK, gin.H{
				"exists":  false,
				"image":   req.Image,
				"message": "镜像不存在于本地",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "DOCKER_ERROR",
			"message": "无法连接到Docker服务",
			"details": string(output),
		})
		return
	}

	var inspectResult []map[string]interface{}
	if err := json.Unmarshal(output, &inspectResult); err == nil && len(inspectResult) > 0 {
		imageInfo := inspectResult[0]
		size := int64(0)
		if s, ok := imageInfo["Size"].(float64); ok {
			size = int64(s)
		}

		c.JSON(http.StatusOK, gin.H{
			"exists":  true,
			"image":   req.Image,
			"message": "镜像存在",
			"size":    FormatBytes(size),
			"id":      imageInfo["Id"],
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"exists":  true,
		"image":   req.Image,
		"message": "镜像存在",
	})
}

// HandleCreateTestContainer 创建测试容器
func HandleCreateTestContainer(c *gin.Context, db *sql.DB) {
	var req DockerCreateContainerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "details": err.Error()})
		return
	}

	fmt.Printf("[DEBUG] CreateTestContainer: Flag=%s, FlagEnvs=%v, FlagScript=%s\n", req.Flag, req.FlagEnvs, req.FlagScript)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	checkCmd := exec.CommandContext(ctx, "docker", "inspect", "--type=image", req.Image)
	if err := checkCmd.Run(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "IMAGE_NOT_FOUND",
			"message": "镜像不存在，请先拉取或构建镜像",
		})
		return
	}

	containerName := fmt.Sprintf("tg_test_%d_%d", req.QuestionID, time.Now().Unix())
	args := []string{"run", "-d", "--name", containerName}

	for _, port := range req.Ports {
		args = append(args, "-p", fmt.Sprintf(":%s", port))
	}

	if req.CPULimit != "" {
		args = append(args, "--cpus", req.CPULimit)
	}
	if req.MemoryLimit != "" {
		args = append(args, "-m", req.MemoryLimit)
	}

	// 处理 Flag 注入方式：环境变量 和/或 命令行参数
	useCmdArg := false
	if req.Flag != "" && len(req.FlagEnvs) > 0 {
		for _, envName := range req.FlagEnvs {
			if envName == "CMDARG" || envName == "$1" {
				useCmdArg = true
			} else {
				args = append(args, "-e", fmt.Sprintf("%s=%s", envName, req.Flag))
			}
		}
	}

	args = append(args, "--label", "tg.type=test")
	args = append(args, "--label", fmt.Sprintf("tg.question_id=%d", req.QuestionID))
	args = append(args, "--rm=false")
	args = append(args, req.Image)

	// 如果配置了 CMDARG，将 flag 作为命令行参数追加到镜像名之后
	if useCmdArg && req.Flag != "" {
		args = append(args, req.Flag)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()

	runCmd := exec.CommandContext(runCtx, "docker", args...)
	output, err := runCmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "CONTAINER_CREATE_FAILED",
			"message": "创建容器失败",
			"details": string(output),
		})
		return
	}

	// 处理可能的警告信息：docker run 输出可能包含 stderr 警告，容器 ID 在最后一行
	outputStr := strings.TrimSpace(string(output))
	lines := strings.Split(outputStr, "\n")
	containerID := strings.TrimSpace(lines[len(lines)-1])

	time.Sleep(500 * time.Millisecond)

	portInfo := make(map[string]string)
	portCmd := exec.CommandContext(runCtx, "docker", "port", containerID)
	portOutput, err := portCmd.CombinedOutput()
	if err == nil {
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
					hostPort := hostAddr[idx+1:]
					portInfo[containerPort] = hostPort
				}
			}
		}
	}

	// 如果配置了 flag_script，在容器启动后执行脚本注入 Flag
	if req.FlagScript != "" && req.Flag != "" {
		fmt.Printf("[DEBUG] Executing flag script: %s\n", req.FlagScript)
		time.Sleep(500 * time.Millisecond) // 等待容器完全启动
		scriptCmd := exec.CommandContext(runCtx, "docker", "exec", containerID, "sh", req.FlagScript, req.Flag)
		scriptOutput, scriptErr := scriptCmd.CombinedOutput()
		if scriptErr != nil {
			fmt.Printf("[DEBUG] Flag script execution failed: %v, output: %s\n", scriptErr, string(scriptOutput))
		} else {
			fmt.Printf("[DEBUG] Flag script executed successfully\n")
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"containerId":   containerID[:12],
		"containerName": containerName,
		"ports":         portInfo,
		"message":       "测试容器创建成功",
	})
}

// HandleListTestContainers 列出测试容器
func HandleListTestContainers(c *gin.Context, db *sql.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=tg.type=test",
		"--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.CreatedAt}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "DOCKER_ERROR",
			"message": "无法获取容器列表",
		})
		return
	}

	var containers []TestContainer
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 5 {
			containers = append(containers, TestContainer{
				ID:        parts[0],
				Name:      parts[1],
				Image:     parts[2],
				Status:    parts[3],
				CreatedAt: parts[5],
			})
		}
	}

	if containers == nil {
		containers = []TestContainer{}
	}

	c.JSON(http.StatusOK, containers)
}

// HandleStopTestContainer 停止测试容器
func HandleStopTestContainer(c *gin.Context, db *sql.DB) {
	containerID := c.Param("id")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stop", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "DOCKER_ERROR",
			"message": "停止容器失败",
			"details": string(output),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "容器已停止"})
}

// HandleRemoveTestContainer 删除测试容器
func HandleRemoveTestContainer(c *gin.Context, db *sql.DB) {
	containerID := c.Param("id")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rmCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	output, err := rmCmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "DOCKER_ERROR",
			"message": "删除容器失败",
			"details": string(output),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "容器已删除"})
}

// HandlePullDockerImage 拉取Docker镜像
func HandlePullDockerImage(c *gin.Context, db *sql.DB) {
	var req DockerTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "details": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "pull", req.Image)
	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "PULL_FAILED",
			"message": "拉取镜像失败",
			"details": string(output),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "镜像拉取成功",
		"image":   req.Image,
	})
}

// FormatBytes 格式化字节大小
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// CheckDockerImageExists 检查Docker镜像是否在本地存在
func CheckDockerImageExists(imageName string) (bool, string) {
	if imageName == "" {
		return false, "镜像名为空"
	}

	cmd := exec.Command("docker", "images", "-q", imageName)
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Docker images check failed for %s: %v", imageName, err)
		return false, "检查失败"
	}

	if len(strings.TrimSpace(string(output))) > 0 {
		return true, "本地存在"
	}
	return false, "本地不存在"
}
