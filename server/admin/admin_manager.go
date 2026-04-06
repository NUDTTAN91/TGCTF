// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package admin

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// AdminDetail 普通管理员详情
type AdminDetail struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Email       *string `json:"email"`
	Status      string  `json:"status"`
	LastLoginIP *string `json:"lastLoginIp"`
	LastLoginAt *string `json:"lastLoginAt"`
	CreatedAt   string  `json:"createdAt"`
}

// AdminPermission 管理员权限
type AdminPermission struct {
	ID           int64   `json:"id"`
	Permission   string  `json:"permission"`
	ResourceType *string `json:"resourceType"`
	ResourceIDs  *string `json:"resourceIds"`
	GrantedBy    *int64  `json:"grantedBy"`
	GrantedAt    string  `json:"grantedAt"`
}

// CreateAdminRequest 创建普通管理员请求
type CreateAdminRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"displayName" binding:"required"`
	Email       string `json:"email"`
	Password    string `json:"password" binding:"required"`
}

// UpdateAdminRequest 更新普通管理员请求
type UpdateAdminRequest struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Status      string `json:"status"`
}

// GrantPermissionRequest 授予权限请求
type GrantPermissionRequest struct {
	Permission   string `json:"permission" binding:"required"`
	ResourceType string `json:"resourceType"`
	ResourceIDs  string `json:"resourceIds"` // 逗号分隔的ID列表，或 "*" 表示全部
}

// HandleListAdmins 获取所有普通管理员列表
func HandleListAdmins(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, username, display_name, email, status,
		       last_login_ip,
		       TO_CHAR(last_login_at, 'YYYY-MM-DD HH24:MI') as last_login_at,
		       TO_CHAR(created_at, 'YYYY-MM-DD HH24:MI') as created_at
		FROM users 
		WHERE role = 'admin'
		ORDER BY id ASC`)
	if err != nil {
		log.Printf("list admins error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var admins []AdminDetail
	for rows.Next() {
		var a AdminDetail
		if err := rows.Scan(&a.ID, &a.Username, &a.DisplayName, &a.Email, &a.Status,
			&a.LastLoginIP, &a.LastLoginAt, &a.CreatedAt); err != nil {
			log.Printf("scan admin error: %v", err)
			continue
		}
		admins = append(admins, a)
	}

	c.JSON(http.StatusOK, gin.H{"admins": admins})
}

// HandleCreateAdmin 创建普通管理员（UID 从 11 开始）
func HandleCreateAdmin(c *gin.Context, db *sql.DB) {
	var req CreateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查用户名是否已存在
	var exists int
	db.QueryRow(`SELECT 1 FROM users WHERE username = $1`, req.Username).Scan(&exists)
	if exists == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "USERNAME_EXISTS"})
		return
	}

	// 找到下一个可用的管理员 UID（11-100）
	var nextID int64
	err := db.QueryRow(`
		SELECT COALESCE(
			(SELECT MIN(t.id) FROM generate_series(11, 100) AS t(id) 
			 WHERE t.id NOT IN (SELECT id FROM users WHERE id >= 11 AND id <= 100)),
			0
		)`).Scan(&nextID)
	if err != nil || nextID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ADMIN_LIMIT_REACHED", "message": "管理员数量已达上限(90个)"})
		return
	}

	// 加密密码
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("hash password error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	var email *string
	if req.Email != "" {
		email = &req.Email
	}

	// 使用指定的 UID 插入管理员
	_, err = db.Exec(`INSERT INTO users (id, username, display_name, email, role, password_hash, status, created_at, updated_at) 
		VALUES ($1, $2, $3, $4, 'admin', $5, 'active', NOW(), NOW())`,
		nextID, req.Username, req.DisplayName, email, string(hash))
	if err != nil {
		log.Printf("create admin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": nextID, "message": "CREATED"})
}

// HandleGetAdmin 获取单个普通管理员详情及其权限
func HandleGetAdmin(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	var admin AdminDetail
	err := db.QueryRow(`
		SELECT id, username, display_name, email, status,
		       last_login_ip,
		       TO_CHAR(last_login_at, 'YYYY-MM-DD HH24:MI') as last_login_at,
		       TO_CHAR(created_at, 'YYYY-MM-DD HH24:MI') as created_at
		FROM users 
		WHERE id = $1 AND role = 'admin'`, id).Scan(
		&admin.ID, &admin.Username, &admin.DisplayName, &admin.Email, &admin.Status,
		&admin.LastLoginIP, &admin.LastLoginAt, &admin.CreatedAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Printf("get admin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	// 获取权限列表
	permRows, err := db.Query(`
		SELECT id, permission, resource_type, resource_ids, granted_by,
		       TO_CHAR(granted_at, 'YYYY-MM-DD HH24:MI') as granted_at
		FROM admin_permissions 
		WHERE user_id = $1
		ORDER BY granted_at DESC`, id)
	if err != nil {
		log.Printf("get admin permissions error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer permRows.Close()

	var permissions []AdminPermission
	for permRows.Next() {
		var p AdminPermission
		if err := permRows.Scan(&p.ID, &p.Permission, &p.ResourceType, &p.ResourceIDs, &p.GrantedBy, &p.GrantedAt); err != nil {
			continue
		}
		permissions = append(permissions, p)
	}

	c.JSON(http.StatusOK, gin.H{"admin": admin, "permissions": permissions})
}

// HandleGetCurrentAdmin 获取当前登录管理员的权限（用于权限检查）
func HandleGetCurrentAdmin(c *gin.Context, db *sql.DB) {
	// 从 context 获取当前用户ID
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}
	userID := userIDVal.(int64)

	// 获取权限列表
	permRows, err := db.Query(`
		SELECT id, permission, resource_type, resource_ids
		FROM admin_permissions 
		WHERE user_id = $1`, userID)
	if err != nil {
		log.Printf("get current admin permissions error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer permRows.Close()

	var permissions []AdminPermission
	for permRows.Next() {
		var p AdminPermission
		if err := permRows.Scan(&p.ID, &p.Permission, &p.ResourceType, &p.ResourceIDs); err != nil {
			continue
		}
		permissions = append(permissions, p)
	}

	c.JSON(http.StatusOK, gin.H{"userId": userID, "permissions": permissions})
}

// HandleUpdateAdmin 更新普通管理员信息
func HandleUpdateAdmin(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为普通管理员
	var role string
	err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AN_ADMIN", "message": "只能修改普通管理员"})
		return
	}

	var req UpdateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	var updates []string
	var args []interface{}
	argIndex := 1

	if req.DisplayName != "" {
		updates = append(updates, "display_name = $"+strconv.Itoa(argIndex))
		args = append(args, req.DisplayName)
		argIndex++
	}
	if req.Email != "" {
		updates = append(updates, "email = $"+strconv.Itoa(argIndex))
		args = append(args, req.Email)
		argIndex++
	}
	if req.Status != "" {
		if req.Status != "active" && req.Status != "banned" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_STATUS"})
			return
		}
		updates = append(updates, "status = $"+strconv.Itoa(argIndex))
		args = append(args, req.Status)
		argIndex++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NO_UPDATES"})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, id)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIndex)
	_, err = db.Exec(query, args...)
	if err != nil {
		log.Printf("update admin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "UPDATED"})
}

// HandleDeleteAdmin 删除普通管理员
func HandleDeleteAdmin(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为普通管理员
	var role string
	err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AN_ADMIN", "message": "只能删除普通管理员"})
		return
	}

	// 先删除权限
	db.Exec(`DELETE FROM admin_permissions WHERE user_id = $1`, id)

	// 删除用户
	_, err = db.Exec(`DELETE FROM users WHERE id = $1 AND role = 'admin'`, id)
	if err != nil {
		log.Printf("delete admin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "DELETED"})
}

// HandleResetAdminPassword 重置普通管理员密码
func HandleResetAdminPassword(c *gin.Context, db *sql.DB) {
	id := c.Param("id")

	// 检查是否为普通管理员
	var role string
	err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AN_ADMIN"})
		return
	}

	// 默认密码 123456
	defaultPassword := "123456"
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	_, err = db.Exec(`UPDATE users SET password_hash = $1, must_change_password = TRUE, 
		token_version = COALESCE(token_version, 1) + 1, updated_at = NOW() WHERE id = $2`, string(hash), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "PASSWORD_RESET", "newPassword": defaultPassword})
}

// HandleGrantPermission 授予普通管理员权限
func HandleGrantPermission(c *gin.Context, db *sql.DB) {
	id := c.Param("id")
	grantedBy := c.GetInt64("userID")

	// 检查是否为普通管理员
	var role string
	err := db.QueryRow(`SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AN_ADMIN"})
		return
	}

	var req GrantPermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 验证权限标识（支持新格式）
	// 有效格式:
	// - contest.menu.view (菜单访问权)
	// - contest.{id}.view (比赛可见)
	// - contest.{id}.monitor (数据大屏)
	// - contest.monitor.view (旧格式，兼容)
	isValid := false
	validPatterns := []string{
		"contest.menu.view",
		"contest.monitor.view", // 旧格式兼容
	}
	for _, p := range validPatterns {
		if p == req.Permission {
			isValid = true
			break
		}
	}
	// 检查 contest.{id}.view 和 contest.{id}.monitor 格式
	if !isValid {
		matched, _ := regexp.MatchString(`^contest\.\d+\.(view|monitor)$`, req.Permission)
		isValid = matched
	}
	// 检查 org.{id}.* 格式
	if !isValid {
		matched, _ := regexp.MatchString(`^org\.\d+\.(view|user\.view|user\.edit|user\.ban|team\.view|team\.edit|team\.ban)$`, req.Permission)
		isValid = matched
	}
	if !isValid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_PERMISSION", "message": "不支持的权限类型"})
		return
	}

	var resourceType, resourceIDs *string
	if req.ResourceType != "" {
		resourceType = &req.ResourceType
	}
	if req.ResourceIDs != "" {
		resourceIDs = &req.ResourceIDs
	}

	// 级联自动授予前置权限（org 权限）
	orgPermRegex := regexp.MustCompile(`^org\.(\d+)\.(.+)$`)
	if matches := orgPermRegex.FindStringSubmatch(req.Permission); matches != nil {
		orgIDStr := matches[1]
		subPerm := matches[2]

		// 1. 任何 org.{id}.* 子权限都需要 org.{id}.view
		if subPerm != "view" {
			autoGrantPerm := fmt.Sprintf("org.%s.view", orgIDStr)
			_, err := db.Exec(`INSERT INTO admin_permissions (user_id, permission, granted_by, granted_at) SELECT $1::bigint, $2::text, $3::bigint, NOW() WHERE NOT EXISTS (SELECT 1 FROM admin_permissions WHERE user_id = $1::bigint AND permission = $2::text AND resource_type IS NULL)`, id, autoGrantPerm, grantedBy)
			if err != nil {
				log.Printf("auto grant org view permission failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
				return
			}
		}

		// 2. user.edit/user.ban 需要 user.view
		if subPerm == "user.edit" || subPerm == "user.ban" {
			autoGrantPerm := fmt.Sprintf("org.%s.user.view", orgIDStr)
			_, err := db.Exec(`INSERT INTO admin_permissions (user_id, permission, granted_by, granted_at) SELECT $1::bigint, $2::text, $3::bigint, NOW() WHERE NOT EXISTS (SELECT 1 FROM admin_permissions WHERE user_id = $1::bigint AND permission = $2::text AND resource_type IS NULL)`, id, autoGrantPerm, grantedBy)
			if err != nil {
				log.Printf("auto grant user view permission failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
				return
			}
		}

		// 3. team.edit/team.ban 需要 team.view
		if subPerm == "team.edit" || subPerm == "team.ban" {
			autoGrantPerm := fmt.Sprintf("org.%s.team.view", orgIDStr)
			_, err := db.Exec(`INSERT INTO admin_permissions (user_id, permission, granted_by, granted_at) SELECT $1::bigint, $2::text, $3::bigint, NOW() WHERE NOT EXISTS (SELECT 1 FROM admin_permissions WHERE user_id = $1::bigint AND permission = $2::text AND resource_type IS NULL)`, id, autoGrantPerm, grantedBy)
			if err != nil {
				log.Printf("auto grant team view permission failed: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
				return
			}
		}
	}

	// 插入或更新权限
	// org 权限的 resource_type 为 NULL，PostgreSQL UNIQUE 约束对 NULL 不生效，需特殊处理
	if orgPermRegex.MatchString(req.Permission) {
		_, err = db.Exec(`INSERT INTO admin_permissions (user_id, permission, granted_by, granted_at) SELECT $1::bigint, $2::text, $3::bigint, NOW() WHERE NOT EXISTS (SELECT 1 FROM admin_permissions WHERE user_id = $1::bigint AND permission = $2::text AND resource_type IS NULL)`,
			id, req.Permission, grantedBy)
	} else {
		_, err = db.Exec(`
		INSERT INTO admin_permissions (user_id, permission, resource_type, resource_ids, granted_by, granted_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id, permission, resource_type) 
		DO UPDATE SET resource_ids = $4, granted_by = $5, granted_at = NOW()`,
			id, req.Permission, resourceType, resourceIDs, grantedBy)
	}
	if err != nil {
		log.Printf("grant permission error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "PERMISSION_GRANTED"})
}

// HandleRevokePermission 撤销普通管理员权限
func HandleRevokePermission(c *gin.Context, db *sql.DB) {
	id := c.Param("id")
	permissionID := c.Param("permissionId")

	// 检查权限是否属于该管理员
	var userID int64
	err := db.QueryRow(`SELECT user_id FROM admin_permissions WHERE id = $1`, permissionID).Scan(&userID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "PERMISSION_NOT_FOUND"})
		return
	}
	if strconv.FormatInt(userID, 10) != id {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PERMISSION_NOT_BELONG"})
		return
	}

	_, err = db.Exec(`DELETE FROM admin_permissions WHERE id = $1`, permissionID)
	if err != nil {
		log.Printf("revoke permission error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "PERMISSION_REVOKED"})
}

// HandleRevokePermissionByName 按权限名称撤销权限（支持级联）
func HandleRevokePermissionByName(c *gin.Context, db *sql.DB) {
	adminID := c.Param("id")

	var req struct {
		Permission string `json:"permission" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	// 检查是否为普通管理员
	var role string
	err := db.QueryRow("SELECT role FROM users WHERE id = $1", adminID).Scan(&role)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}
	if role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "NOT_AN_ADMIN"})
		return
	}

	// 确定需要级联删除的权限列表
	permsToDelete := []string{req.Permission}

	orgPermRegex := regexp.MustCompile(`^org\.(\d+)\.(.+)$`)
	if matches := orgPermRegex.FindStringSubmatch(req.Permission); matches != nil {
		orgIDStr := matches[1]
		subPerm := matches[2]

		if subPerm == "view" {
			// 撤销 org.{id}.view → 级联全部 6 个子权限
			permsToDelete = append(permsToDelete,
				fmt.Sprintf("org.%s.user.view", orgIDStr),
				fmt.Sprintf("org.%s.user.edit", orgIDStr),
				fmt.Sprintf("org.%s.user.ban", orgIDStr),
				fmt.Sprintf("org.%s.team.view", orgIDStr),
				fmt.Sprintf("org.%s.team.edit", orgIDStr),
				fmt.Sprintf("org.%s.team.ban", orgIDStr),
			)
		} else if subPerm == "user.view" {
			// 撤销 user.view → 级联 user.edit + user.ban
			permsToDelete = append(permsToDelete,
				fmt.Sprintf("org.%s.user.edit", orgIDStr),
				fmt.Sprintf("org.%s.user.ban", orgIDStr),
			)
		} else if subPerm == "team.view" {
			// 撤销 team.view → 级联 team.edit + team.ban
			permsToDelete = append(permsToDelete,
				fmt.Sprintf("org.%s.team.edit", orgIDStr),
				fmt.Sprintf("org.%s.team.ban", orgIDStr),
			)
		}
	}

	// 批量删除
	totalDeleted := int64(0)
	for _, perm := range permsToDelete {
		result, err := db.Exec("DELETE FROM admin_permissions WHERE user_id = $1 AND permission = $2", adminID, perm)
		if err != nil {
			log.Printf("revoke permission '%s' for admin %s error: %v", perm, adminID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}
		if n, _ := result.RowsAffected(); n > 0 {
			totalDeleted += n
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "PERMISSION_REVOKED", "revokedCount": totalDeleted})
}

// HasPermission 检查管理员是否拥有指定权限（简化版，不需要 resource_type/resource_ids）
func HasPermission(db *sql.DB, userID int64, role string, permission string) bool {
	if role == "super" {
		return true
	}
	var exists int
	err := db.QueryRow("SELECT 1 FROM admin_permissions WHERE user_id = $1 AND permission = $2", userID, permission).Scan(&exists)
	return err == nil && exists == 1
}

// HandleGetMyOrganizations 获取当前管理员有权限的组织列表
func HandleGetMyOrganizations(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")

	// 超管返回所有组织
	if role == "super" {
		rows, err := db.Query(`
			SELECT o.id, o.name, o.description, o.status,
			       (SELECT COUNT(*) FROM users WHERE organization_id = o.id) as user_count,
			       (SELECT COUNT(*) FROM teams WHERE organization_id = o.id) as team_count
			FROM organizations o
			ORDER BY o.id ASC`)
		if err != nil {
			log.Printf("get all organizations error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}
		defer rows.Close()

		var orgs []gin.H
		for rows.Next() {
			var id int64
			var name, status string
			var description sql.NullString
			var userCount, teamCount int
			rows.Scan(&id, &name, &description, &status, &userCount, &teamCount)
			org := gin.H{
				"id":          id,
				"name":        name,
				"description": "",
				"status":      status,
				"userCount":   userCount,
				"teamCount":   teamCount,
				"permissions": []string{"view", "user.view", "user.edit", "user.ban", "team.view", "team.edit", "team.ban"},
			}
			if description.Valid {
				org["description"] = description.String
			}
			orgs = append(orgs, org)
		}
		c.JSON(http.StatusOK, gin.H{"organizations": orgs})
		return
	}

	// 查询该管理员所有 org.* 权限
	rows, err := db.Query("SELECT permission FROM admin_permissions WHERE user_id = $1 AND permission LIKE 'org.%'", userID)
	if err != nil {
		log.Printf("get my org permissions error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	// 解析出 org_id → permissions 映射
	orgPermRegex := regexp.MustCompile(`^org\.(\d+)\.(.+)$`)
	orgPerms := make(map[string][]string) // orgID -> [view, user.view, user.edit, ...]
	for rows.Next() {
		var perm string
		rows.Scan(&perm)
		matches := orgPermRegex.FindStringSubmatch(perm)
		if matches != nil {
			orgID := matches[1]
			subPerm := matches[2]
			orgPerms[orgID] = append(orgPerms[orgID], subPerm)
		}
	}

	// 只保留有 view 权限的组织
	var orgIDs []string
	for orgID, perms := range orgPerms {
		for _, p := range perms {
			if p == "view" {
				orgIDs = append(orgIDs, orgID)
				break
			}
		}
	}

	if len(orgIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"organizations": []interface{}{}})
		return
	}

	// 构建 IN 查询获取组织详情
	placeholders := make([]string, len(orgIDs))
	args := make([]interface{}, len(orgIDs))
	for i, oid := range orgIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = oid
	}

	query := fmt.Sprintf(`
		SELECT o.id, o.name, o.description, o.status,
		       (SELECT COUNT(*) FROM users WHERE organization_id = o.id) as user_count,
		       (SELECT COUNT(*) FROM teams WHERE organization_id = o.id) as team_count
		FROM organizations o
		WHERE o.id IN (%s)
		ORDER BY o.id ASC`, strings.Join(placeholders, ","))

	orgRows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("get my organizations error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer orgRows.Close()

	var orgs []gin.H
	for orgRows.Next() {
		var id int64
		var name, status string
		var description sql.NullString
		var userCount, teamCount int
		orgRows.Scan(&id, &name, &description, &status, &userCount, &teamCount)
		org := gin.H{
			"id":          id,
			"name":        name,
			"description": "",
			"status":      status,
			"userCount":   userCount,
			"teamCount":   teamCount,
			"permissions": orgPerms[strconv.FormatInt(id, 10)],
		}
		if description.Valid {
			org["description"] = description.String
		}
		orgs = append(orgs, org)
	}

	c.JSON(http.StatusOK, gin.H{"organizations": orgs})
}

// HandleGetOrganizationsForPermission 获取所有组织列表（用于权限分配时选择）
func HandleGetOrganizationsForPermission(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT o.id, o.name, o.status,
		       (SELECT COUNT(*) FROM users WHERE organization_id = o.id) as user_count,
		       (SELECT COUNT(*) FROM teams WHERE organization_id = o.id) as team_count
		FROM organizations o
		ORDER BY o.id ASC`)
	if err != nil {
		log.Printf("get organizations for permission error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	type OrgInfo struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
		UserCount int    `json:"userCount"`
		TeamCount int    `json:"teamCount"`
	}

	var orgs []OrgInfo
	for rows.Next() {
		var o OrgInfo
		rows.Scan(&o.ID, &o.Name, &o.Status, &o.UserCount, &o.TeamCount)
		orgs = append(orgs, o)
	}

	c.JSON(http.StatusOK, gin.H{"organizations": orgs})
}

// HandleGetMyPermissions 获取当前管理员的权限列表（用于前端判断）
func HandleGetMyPermissions(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")

	// 超管拥有所有权限
	if role == "super" {
		c.JSON(http.StatusOK, gin.H{
			"role":        "super",
			"permissions": []string{"*"}, // 通配符表示全部权限
		})
		return
	}

	// 查询普通管理员的权限
	rows, err := db.Query(`
		SELECT permission, resource_type, resource_ids 
		FROM admin_permissions 
		WHERE user_id = $1`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	type PermissionInfo struct {
		Permission   string  `json:"permission"`
		ResourceType *string `json:"resourceType"`
		ResourceIDs  *string `json:"resourceIds"`
	}

	var permissions []PermissionInfo
	for rows.Next() {
		var p PermissionInfo
		rows.Scan(&p.Permission, &p.ResourceType, &p.ResourceIDs)
		permissions = append(permissions, p)
	}

	c.JSON(http.StatusOK, gin.H{
		"role":        "admin",
		"permissions": permissions,
	})
}

// HandleGetMyContests 获取当前管理员有权限访问的比赛列表
func HandleGetMyContests(c *gin.Context, db *sql.DB) {
	userID := c.GetInt64("userID")
	role := c.GetString("role")

	// 超管可以看到所有比赛
	if role == "super" {
		rows, err := db.Query(`
			SELECT id, name, mode, status,
			       TO_CHAR(start_time, 'YYYY-MM-DD HH24:MI') as start_time,
			       TO_CHAR(end_time, 'YYYY-MM-DD HH24:MI') as end_time
			FROM contests ORDER BY id DESC`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
			return
		}
		defer rows.Close()

		var contests []gin.H
		for rows.Next() {
			var id int64
			var name, mode, status, startTime, endTime string
			rows.Scan(&id, &name, &mode, &status, &startTime, &endTime)
			contests = append(contests, gin.H{
				"id": id, "name": name, "mode": mode, "status": status,
				"startTime": startTime, "endTime": endTime,
			})
		}
		c.JSON(http.StatusOK, gin.H{"contests": contests})
		return
	}

	// 普通管理员只能看到有权限的比赛
	// 先查询有权限的比赛ID
	permRows, _ := db.Query(`
		SELECT permission FROM admin_permissions 
		WHERE user_id = $1 AND permission LIKE 'contest.%.view'`, userID)
	
	contestIDs := make(map[int64]bool)
	if permRows != nil {
		for permRows.Next() {
			var perm string
			permRows.Scan(&perm)
			// 解析 contest.{id}.view 格式
			var cid int64
			if _, err := fmt.Sscanf(perm, "contest.%d.view", &cid); err == nil {
				contestIDs[cid] = true
			}
		}
		permRows.Close()
	}

	if len(contestIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"contests": []gin.H{}})
		return
	}

	// 构建IN查询
	ids := make([]interface{}, 0, len(contestIDs))
	placeholders := make([]string, 0, len(contestIDs))
	i := 1
	for id := range contestIDs {
		ids = append(ids, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		i++
	}

	query := fmt.Sprintf(`
		SELECT id, name, mode, status,
		       TO_CHAR(start_time, 'YYYY-MM-DD HH24:MI') as start_time,
		       TO_CHAR(end_time, 'YYYY-MM-DD HH24:MI') as end_time
		FROM contests WHERE id IN (%s) ORDER BY id DESC`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, ids...)
	if err != nil {
		log.Printf("get my contests error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	var contests []gin.H
	for rows.Next() {
		var id int64
		var name, mode, status, startTime, endTime string
		rows.Scan(&id, &name, &mode, &status, &startTime, &endTime)
		contests = append(contests, gin.H{
			"id": id, "name": name, "mode": mode, "status": status,
			"startTime": startTime, "endTime": endTime,
		})
	}

	c.JSON(http.StatusOK, gin.H{"contests": contests})
}

// HandleGetAllContestsForPermission 获取所有比赛列表（用于权限分配时选择）
func HandleGetAllContestsForPermission(c *gin.Context, db *sql.DB) {
	rows, err := db.Query(`
		SELECT id, name, mode, status,
		       TO_CHAR(start_time, 'YYYY-MM-DD HH24:MI') as start_time,
		       TO_CHAR(end_time, 'YYYY-MM-DD HH24:MI') as end_time
		FROM contests
		ORDER BY id DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	type ContestInfo struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Mode      string `json:"mode"`
		Status    string `json:"status"`
		StartTime string `json:"startTime"`
		EndTime   string `json:"endTime"`
	}

	var contests []ContestInfo
	for rows.Next() {
		var c ContestInfo
		rows.Scan(&c.ID, &c.Name, &c.Mode, &c.Status, &c.StartTime, &c.EndTime)
		contests = append(contests, c)
	}

	c.JSON(http.StatusOK, gin.H{"contests": contests})
}

// CheckAdminPermission 检查管理员是否拥有指定权限（辅助函数）
func CheckAdminPermission(db *sql.DB, userID int64, role string, permission string, resourceType string, resourceID string) bool {
	// 超管拥有所有权限
	if role == "super" {
		return true
	}

	// 查询权限
	var resourceIDs sql.NullString
	err := db.QueryRow(`
		SELECT resource_ids FROM admin_permissions 
		WHERE user_id = $1 AND permission = $2 AND (resource_type = $3 OR resource_type IS NULL)`,
		userID, permission, resourceType).Scan(&resourceIDs)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		log.Printf("check permission error: %v", err)
		return false
	}

	// 如果 resource_ids 为 * 表示全部权限
	if !resourceIDs.Valid || resourceIDs.String == "*" {
		return true
	}

	// 检查资源ID是否在列表中
	ids := strings.Split(resourceIDs.String, ",")
	for _, id := range ids {
		if strings.TrimSpace(id) == resourceID {
			return true
		}
	}

	return false
}
