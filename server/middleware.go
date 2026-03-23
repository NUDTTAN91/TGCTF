// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package main

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// authMiddleware JWT认证中间件（仅超级管理员）
func authMiddleware(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 优先从Authorization头获取token，如果没有则从查询参数获取（用于文件下载）
		authHeader := c.GetHeader("Authorization")
		var tokenString string
		if authHeader != "" {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			// 从查询参数获取token（用于文件下载等场景）
			tokenString = c.Query("token")
		}
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
			c.Abort()
			return
		}
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return secret, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_TOKEN"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CLAIMS"})
			c.Abort()
			return
		}

		// 检查是否为超级管理员
		role, _ := claims["role"].(string)
		if role != "super" {
			c.JSON(http.StatusForbidden, gin.H{"error": "FORBIDDEN"})
			c.Abort()
			return
		}

		// 从 claims 中提取用户ID
		var userID int64
		if sub, ok := claims["sub"].(float64); ok {
			userID = int64(sub)
		}

		c.Set("claims", claims)
		c.Set("role", role)
		c.Set("userID", userID)
		c.Next()
	}
}

// adminAuthMiddleware JWT认证中间件（超级管理员和普通管理员）
// 用于管理后台访问，不检查具体权限，只验证是管理员身份
func adminAuthMiddleware(secret []byte, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		var tokenString string
		if authHeader != "" {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			tokenString = c.Query("token")
		}
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
			c.Abort()
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return secret, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_TOKEN"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CLAIMS"})
			c.Abort()
			return
		}

		role, _ := claims["role"].(string)
		// 检查是否为管理员（super 或 admin）
		if role != "super" && role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "FORBIDDEN", "message": "仅管理员可访问"})
			c.Abort()
			return
		}

		// 从 claims 中提取用户ID
		var userID int64
		if sub, ok := claims["sub"].(float64); ok {
			userID = int64(sub)
		}

		// 验证 token_version
		var dbTokenVersion int
		err = db.QueryRow(`SELECT COALESCE(token_version, 1) FROM users WHERE id = $1`, userID).Scan(&dbTokenVersion)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "USER_NOT_FOUND"})
			c.Abort()
			return
		}

		tokenVersion := 1
		if tv, ok := claims["tokenVersion"].(float64); ok {
			tokenVersion = int(tv)
		}
		if tokenVersion != dbTokenVersion {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "TOKEN_EXPIRED", "message": "登录已失效，请重新登录"})
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Set("role", role)
		c.Set("userID", userID)
		c.Next()
	}
}

// userAuthMiddleware JWT认证中间件（所有登录用户）
func userAuthMiddleware(secret []byte, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return secret, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_TOKEN"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CLAIMS"})
			c.Abort()
			return
		}

		role, _ := claims["role"].(string)
		// 从 claims 中提取用户ID
		var userID int64
		if sub, ok := claims["sub"].(float64); ok {
			userID = int64(sub)
		}

		// 验证 token_version，确保 token 未被失效
		var dbTokenVersion int
		err = db.QueryRow(`SELECT COALESCE(token_version, 1) FROM users WHERE id = $1`, userID).Scan(&dbTokenVersion)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "USER_NOT_FOUND"})
			c.Abort()
			return
		}

		// 检查 token 中的版本号是否与数据库一致
		tokenVersion := 1
		if tv, ok := claims["tokenVersion"].(float64); ok {
			tokenVersion = int(tv)
		}
		if tokenVersion != dbTokenVersion {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "TOKEN_EXPIRED", "message": "登录已失效，请重新登录"})
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Set("role", role)
		c.Set("userID", userID)
		c.Next()
	}
}
