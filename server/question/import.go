// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package question

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"tgctf/server/docker"
)

// HandleImportQuestions 批量导入题库题目
func HandleImportQuestions(c *gin.Context, db *sql.DB) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_FILE", "message": "请上传Excel文件"})
		return
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "FILE_OPEN_ERROR"})
		return
	}
	defer src.Close()

	// 解析Excel
	f, err := excelize.OpenReader(src)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "EXCEL_PARSE_ERROR", "message": "无法解析Excel文件"})
		return
	}
	defer f.Close()

	// 获取第一个sheet
	sheetName := f.GetSheetName(0)
	rows, err := f.GetRows(sheetName)
	if err != nil || len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "EMPTY_FILE", "message": "Excel文件为空或格式错误"})
		return
	}

	// 读取所有类别（用于名称转ID）
	categoryMap := make(map[string]int64)
	catRows, _ := db.Query("SELECT id, name FROM categories")
	if catRows != nil {
		defer catRows.Close()
		for catRows.Next() {
			var id int64
			var name string
			catRows.Scan(&id, &name)
			categoryMap[strings.ToUpper(name)] = id
		}
	}

	// 解析表头（第一行）
	headers := rows[0]
	headerIndex := make(map[string]int)
	for i, h := range headers {
		headerIndex[strings.ToLower(strings.TrimSpace(h))] = i
	}

	// 导入结果
	type ImportResult struct {
		Row       int    `json:"row"`
		Title     string `json:"title"`
		Success   bool   `json:"success"`
		Message   string `json:"message"`
		NeedsEdit bool   `json:"needsEdit"`
	}
	var results []ImportResult
	successCount := 0
	failCount := 0
	needsEditCount := 0

	// 遍历数据行
	for rowIndex := 1; rowIndex < len(rows); rowIndex++ {
		row := rows[rowIndex]
		result := ImportResult{Row: rowIndex + 1}

		// 获取字段值（支持模糊匹配表头，因为表头可能带括号说明）
		getValue := func(fields ...string) string {
			for _, field := range fields {
				fieldLower := strings.ToLower(field)
				// 先精确匹配
				if idx, ok := headerIndex[fieldLower]; ok && idx < len(row) {
					return strings.TrimSpace(row[idx])
				}
				// 再模糊匹配（表头包含关键词）
				for header, idx := range headerIndex {
					if strings.Contains(header, fieldLower) && idx < len(row) {
						return strings.TrimSpace(row[idx])
					}
				}
			}
			return ""
		}

		// 题目标题
		title := getValue("题目标题", "题目名称", "title")
		if title == "" {
			result.Message = "题目标题不能为空"
			results = append(results, result)
			failCount++
			continue
		}
		result.Title = title

		// 题目类型
		qType := getValue("题目类型", "type")
		if qType == "" {
			qType = "static_attachment" // 默认
		}
		// 转换类型名称
		typeMap := map[string]string{
			"静态附件":             "static_attachment",
			"静态容器":             "static_container",
			"动态附件":             "dynamic_attachment",
			"动态容器":             "dynamic_container",
			"static_attachment":  "static_attachment",
			"static_container":   "static_container",
			"dynamic_attachment": "dynamic_attachment",
			"dynamic_container":  "dynamic_container",
		}
		if mapped, ok := typeMap[qType]; ok {
			qType = mapped
		} else {
			result.Message = "无效的题目类型: " + qType
			results = append(results, result)
			failCount++
			continue
		}

		// 类别
		category := strings.ToUpper(getValue("题目类别", "类别", "category"))
		categoryID, ok := categoryMap[category]
		if !ok || category == "" {
			// 类别不存在或留空，自动归为 other
			if otherID, ok := categoryMap["OTHER"]; ok {
				categoryID = otherID
			} else if miscID, ok := categoryMap["MISC"]; ok {
				categoryID = miscID
			} else {
				result.Message = "无效的类别且找不到OTHER/MISC: " + category
				results = append(results, result)
				failCount++
				continue
			}
		}

		// 题目难度 (1-10星)
		difficultyStr := getValue("题目难度", "难度", "difficulty")
		difficulty := 5 // 默认
		if difficultyStr != "" {
			if d, err := parseIntDefault(difficultyStr, 5); err == nil && d >= 1 && d <= 10 {
				difficulty = d
			}
		}

		// 题目描述
		description := getValue("题目描述", "描述", "description")

		// FLAG设置
		flag := getValue("flag设置", "flag", "flag")

		// Docker镜像名
		dockerImage := getValue("docker镜像名", "镜像", "镜像名", "docker_image", "image")

		// 服务端口（转换为JSON数组格式）
		portsRaw := getValue("服务端口", "端口", "ports")
		var ports string
		if portsRaw != "" {
			// 支持多种格式: "80" 或 "80,443" 或 "80, 443"
			portList := []string{}
			for _, p := range strings.Split(portsRaw, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					portList = append(portList, p)
				}
			}
			if len(portList) > 0 {
				portsJSON, _ := json.Marshal(portList)
				ports = string(portsJSON)
			}
		}

		// 是否有附件 (1=是, 2=否)
		hasAttachment := getValue("是否有附件", "有附件") == "1"

		// CPU限制 (0=不限制)
		cpuLimitRaw := getValue("cpu", "cpu限制")
		cpuLimit := cpuLimitRaw
		if cpuLimit == "0" {
			cpuLimit = ""
		}

		// 内存限制 (0=不限制)
		memoryLimitRaw := getValue("内存", "内存限制", "memory")
		memoryLimit := memoryLimitRaw
		if memoryLimit == "0" {
			memoryLimit = ""
		}

		// 存储限制 (0=不限制)
		storageLimitRaw := getValue("存储", "存储限制", "storage")
		storageLimit := storageLimitRaw
		if storageLimit == "0" {
			storageLimit = ""
		}

		// 判断是否不限制资源（CPU/内存/存储都为0或空）
		noResourceLimit := (cpuLimitRaw == "0" || cpuLimitRaw == "") &&
			(memoryLimitRaw == "0" || memoryLimitRaw == "") &&
			(storageLimitRaw == "0" || storageLimitRaw == "")

		// FLAG注入方式 (默认FLAG)
		flagEnv := getValue("flag注入方式", "注入方式", "flag_env")
		if flagEnv == "" {
			flagEnv = "FLAG"
		}
		// 验证FLAG注入方式
		validFlagEnvs := map[string]bool{
			"FLAG": true, "GZCTF_FLAG": true, "CTF_FLAG": true, "DYNAMIC_FLAG": true,
		}
		if !validFlagEnvs[strings.ToUpper(flagEnv)] {
			flagEnv = "FLAG" // 无效则默认为FLAG
		} else {
			flagEnv = strings.ToUpper(flagEnv)
		}

		// 确定flag类型
		flagType := "static"
		if qType == "dynamic_attachment" || qType == "dynamic_container" {
			flagType = "dynamic"
		}

		// 判断是否需要再次编辑（有附件需要后续上传）
		needsEdit := hasAttachment
		if needsEdit {
			needsEditCount++
		}
		result.NeedsEdit = needsEdit

		// 插入数据库
		_, err := db.Exec(`
			INSERT INTO question_bank (
				title, type, category_id, difficulty, description, 
				flag, flag_type, docker_image, ports,
				cpu_limit, memory_limit, storage_limit, no_resource_limit, flag_env, needs_edit
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		`, title, qType, categoryID, difficulty, NullIfEmpty(description),
			NullIfEmpty(flag), flagType, NullIfEmpty(dockerImage), NullIfEmpty(ports),
			NullIfEmpty(cpuLimit), NullIfEmpty(memoryLimit), NullIfEmpty(storageLimit),
			noResourceLimit, flagEnv, needsEdit)

		if err != nil {
			result.Message = "数据库错误: " + err.Error()
			results = append(results, result)
			failCount++
			continue
		}

		result.Success = true
		if needsEdit {
			result.Message = "导入成功，需要再次编辑"
		} else {
			result.Message = "导入成功"
		}
		results = append(results, result)
		successCount++
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        successCount,
		"fail":           failCount,
		"total":          successCount + failCount,
		"needsEditCount": needsEditCount,
		"results":        results,
	})
}

// HandleDownloadQuestionTemplate 下载题库导入模板
func HandleDownloadQuestionTemplate(c *gin.Context, db *sql.DB) {
	f := excelize.NewFile()
	sheetName := "Sheet1"

	// 设置表头
	headers := []string{
		"题目标题", "题目类型", "题目类别（输入的不存在或留空则导入时自动归为OTHER）", "题目难度（1~10星）", "题目描述",
		"FLAG设置", "Docker镜像名", "服务端口（如80或多端口80,443）", "是否有附件（1是2不是）",
		"CPU（0为不限制）", "内存（0为不限制）", "存储（0为不限制）", "FLAG注入方式（输入的不存在或留空则导入时自动归为FLAG）",
	}

	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheetName, cell, h)
	}

	// 设置表头样式
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"FF6B00"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	f.SetCellStyle(sheetName, "A1", "M1", headerStyle)

	// 设置列宽
	f.SetColWidth(sheetName, "A", "A", 20) // 题目标题
	f.SetColWidth(sheetName, "B", "B", 12) // 题目类型
	f.SetColWidth(sheetName, "C", "C", 40) // 题目类别
	f.SetColWidth(sheetName, "D", "D", 18) // 题目难度
	f.SetColWidth(sheetName, "E", "E", 30) // 题目描述
	f.SetColWidth(sheetName, "F", "F", 25) // FLAG设置
	f.SetColWidth(sheetName, "G", "G", 25) // Docker镜像名
	f.SetColWidth(sheetName, "H", "H", 28) // 服务端口
	f.SetColWidth(sheetName, "I", "I", 22) // 是否有附件
	f.SetColWidth(sheetName, "J", "J", 18) // CPU
	f.SetColWidth(sheetName, "K", "K", 18) // 内存
	f.SetColWidth(sheetName, "L", "L", 18) // 存储
	f.SetColWidth(sheetName, "M", "M", 42) // FLAG注入方式

	// 题目类型列添加下拉菜单 (B列，第2行到第1000行)
	dvType := excelize.NewDataValidation(true)
	dvType.Sqref = "B2:B1000"
	dvType.SetDropList([]string{"静态附件", "静态容器", "动态附件", "动态容器"})
	f.AddDataValidation(sheetName, dvType)

	// 添加示例数据
	examples := [][]interface{}{
		{"示例题目1-静态附件", "静态附件", "WEB", 3, "这是一道静态附件题目", "TG{example_flag_1}", "", "", 2, 0, 0, 0, "FLAG"},
		{"示例题目2-动态容器", "动态容器", "PWN", 7, "这是一道动态容器题目", "", "ctftraining/base_image_nginx_mysql_php_74:latest", "80", 2, "1.0", "512m", "1g", "FLAG"},
		{"示例题目3-多端口", "动态容器", "MISC", 5, "多端口题目示例", "", "nginx:latest", "80,443", 1, 0, 0, 0, "GZCTF_FLAG"},
	}

	for i, row := range examples {
		for j, val := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue(sheetName, cell, val)
		}
	}

	// 创建说明工作表
	f.NewSheet("填写说明")
	instructions := [][]string{
		{"字段名", "说明", "示例值"},
		{"题目标题", "必填，题目名称", "示例题目1"},
		{"题目类型", "可选：静态附件/静态容器/动态附件/动态容器", "动态容器"},
		{"题目类别", "留空或不存在的类别自动归为OTHER", "WEB"},
		{"题目难度", "1~10星，默认5", "5"},
		{"题目描述", "题目的详细描述", "这是题目描述..."},
		{"FLAG设置", "静态题目的FLAG，动态题目可留空", "TG{flag_here}"},
		{"Docker镜像名", "容器题目的Docker镜像", "nginx:latest"},
		{"服务端口", "容器暴露的端口，多端口用逗号分隔", "80 或 80,443"},
		{"是否有附件", "1=是，2=否，填1则需要导入后再编辑上传附件", "2"},
		{"CPU", "CPU限制，0表示不限制", "1.0"},
		{"内存", "内存限制，0表示不限制", "512m"},
		{"存储", "存储限制，0表示不限制", "1g"},
		{"FLAG注入方式", "留空或不存在默认为FLAG，可选:FLAG/GZCTF_FLAG/CTF_FLAG/DYNAMIC_FLAG", "FLAG"},
	}

	for i, row := range instructions {
		for j, val := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			f.SetCellValue("填写说明", cell, val)
		}
	}

	// 设置说明页样式
	f.SetColWidth("填写说明", "A", "A", 15)
	f.SetColWidth("填写说明", "B", "B", 50)
	f.SetColWidth("填写说明", "C", "C", 30)
	headerStyle2, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"EEEEEE"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	f.SetCellStyle("填写说明", "A1", "C1", headerStyle2)

	// 返回文件
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=question_import_template.xlsx")

	if err := f.Write(c.Writer); err != nil {
		log.Printf("write excel error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "WRITE_ERROR"})
		return
	}
}

// parseIntDefault 解析整数，失败返回默认值
func parseIntDefault(s string, defaultVal int) (int, error) {
	if s == "" {
		return defaultVal, nil
	}
	var val int
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil {
		return defaultVal, err
	}
	return val, nil
}

// HandleBatchTestImages 批量测试镜像是否存在
func HandleBatchTestImages(c *gin.Context, db *sql.DB) {
	var req struct {
		QuestionIDs []int64 `json:"questionIds"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		// 如果没有指定ID，测试所有容器题目
		req.QuestionIDs = nil
	}

	// 查询需要测试的题目
	var query string
	var args []interface{}
	if len(req.QuestionIDs) > 0 {
		query = `SELECT id, title, docker_image FROM question_bank 
			WHERE docker_image IS NOT NULL AND docker_image != '' AND id = ANY($1)`
		args = append(args, req.QuestionIDs)
	} else {
		query = `SELECT id, title, docker_image FROM question_bank 
			WHERE docker_image IS NOT NULL AND docker_image != ''`
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DATABASE_ERROR"})
		return
	}
	defer rows.Close()

	type TestResult struct {
		QuestionID  int64  `json:"questionId"`
		Title       string `json:"title"`
		DockerImage string `json:"dockerImage"`
		Exists      bool   `json:"exists"`
		Message     string `json:"message"`
	}

	var results []TestResult
	existsCount := 0
	notFoundCount := 0

	for rows.Next() {
		var r TestResult
		if err := rows.Scan(&r.QuestionID, &r.Title, &r.DockerImage); err != nil {
			continue
		}

		// 检查镜像是否存在
		r.Exists, r.Message = docker.CheckDockerImageExists(r.DockerImage)
		if r.Exists {
			existsCount++
		} else {
			notFoundCount++
		}
		results = append(results, r)

		// 保存状态到数据库
		status := "not_found"
		if r.Exists {
			status = "exists"
		}
		db.Exec(`UPDATE question_bank SET image_status = $1, image_checked_at = CURRENT_TIMESTAMP WHERE id = $2`, status, r.QuestionID)
	}

	if results == nil {
		results = []TestResult{}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":    len(results),
		"exists":   existsCount,
		"notFound": notFoundCount,
		"results":  results,
	})
}
