// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package main

import (
	"database/sql"

	"github.com/gin-gonic/gin"

	"tgctf/server/authz"
)

// requireRole 构造鉴权中间件：先校验身份，再要求数据库中的角色落在 allowed 内。
// allowed 为空表示任何已登录用户均可访问。
// allowQuery 用于放行无法自定义请求头的场景（如浏览器直接下载附件）。
func requireRole(secret []byte, db *sql.DB, allowQuery bool, forbiddenMsg string, allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, authErr := authz.Authenticate(c, db, secret, allowQuery)
		if authErr != nil {
			authz.Abort(c, authErr)
			return
		}

		if !authz.HasRole(identity.Role, allowed) {
			authz.Abort(c, authz.Forbidden(forbiddenMsg))
			return
		}

		identity.Bind(c)
		c.Next()
	}
}

// authMiddleware 仅超级管理员可访问
func authMiddleware(secret []byte, db *sql.DB) gin.HandlerFunc {
	return requireRole(secret, db, true, "", "super")
}

// adminAuthMiddleware 超级管理员与普通管理员均可访问。
// 只验证管理员身份，具体的资源级权限由各处理函数自行判断。
func adminAuthMiddleware(secret []byte, db *sql.DB) gin.HandlerFunc {
	return requireRole(secret, db, true, "仅管理员可访问", "super", "admin")
}

// userAuthMiddleware 所有已登录用户均可访问
func userAuthMiddleware(secret []byte, db *sql.DB) gin.HandlerFunc {
	return requireRole(secret, db, false, "")
}
