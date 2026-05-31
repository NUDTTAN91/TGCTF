// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

// Package authz 提供统一的请求身份校验。
// token 只用来确认"请求者是谁"，能否继续访问一律以数据库中账号的当前状态为准，
// 这样封禁、改密、降权等操作无需在每个写入口逐个补代码即可立即生效。
package authz

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Identity 经 token 与数据库双重校验后得到的请求身份
type Identity struct {
	UserID int64
	Role   string // 以数据库为准，避免 token 中已过期的角色继续生效
	Claims jwt.MapClaims
}

// Error 鉴权失败的原因，携带响应状态码与错误码
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code }

// 账号或队伍被封禁同样返回 401，以便前端既有的"401 即清除 token 回登录页"逻辑
// 直接把人踢出去；403 会被部分页面当作"权限不足"而停留在原页面。
var (
	ErrMissingToken  = &Error{http.StatusUnauthorized, "UNAUTHORIZED", ""}
	ErrInvalidToken  = &Error{http.StatusUnauthorized, "INVALID_TOKEN", ""}
	ErrInvalidClaims = &Error{http.StatusUnauthorized, "INVALID_CLAIMS", ""}
	ErrUserNotFound  = &Error{http.StatusUnauthorized, "USER_NOT_FOUND", ""}
	ErrTokenExpired  = &Error{http.StatusUnauthorized, "TOKEN_EXPIRED", "登录已失效，请重新登录"}
	ErrAccountBanned = &Error{http.StatusUnauthorized, "ACCOUNT_DISABLED", "该账号已被封禁，请联系管理员"}
	ErrTeamBanned    = &Error{http.StatusUnauthorized, "TEAM_DISABLED", "所属队伍已被封禁，请联系管理员"}
)

// Forbidden 构造一个角色不满足要求的错误
func Forbidden(message string) *Error {
	return &Error{http.StatusForbidden, "FORBIDDEN", message}
}

// TokenFromRequest 取出请求携带的 token。
// allowQuery 只在无法自定义请求头的场景开启（WebSocket、浏览器直接下载附件）。
func TokenFromRequest(c *gin.Context, allowQuery bool) string {
	if header := c.GetHeader("Authorization"); header != "" {
		return strings.TrimPrefix(header, "Bearer ")
	}
	if allowQuery {
		return c.Query("token")
	}
	return ""
}

// ParseToken 校验 JWT 的签名算法、签名与有效期。
// 显式限定算法，避免解析方接受签发时未使用的算法。
func ParseToken(tokenString string, secret []byte) (jwt.MapClaims, *Error) {
	token, err := jwt.Parse(tokenString, func(*jwt.Token) (interface{}, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidClaims
	}
	return claims, nil
}

// Authenticate 解析 token 并核对账号在数据库中的当前状态
func Authenticate(c *gin.Context, db *sql.DB, secret []byte, allowQuery bool) (*Identity, *Error) {
	tokenString := TokenFromRequest(c, allowQuery)
	if tokenString == "" {
		return nil, ErrMissingToken
	}

	claims, authErr := ParseToken(tokenString, secret)
	if authErr != nil {
		return nil, authErr
	}

	var userID int64
	if sub, ok := claims["sub"].(float64); ok {
		userID = int64(sub)
	}
	if userID == 0 {
		return nil, ErrInvalidClaims
	}

	var (
		role         string
		status       string
		tokenVersion int
		teamStatus   string
	)
	err := db.QueryRow(`
		SELECT u.role, COALESCE(u.status, 'active'), COALESCE(u.token_version, 1),
		       COALESCE(t.status, 'active')
		FROM users u
		LEFT JOIN teams t ON u.team_id = t.id
		WHERE u.id = $1`, userID).Scan(&role, &status, &tokenVersion, &teamStatus)
	if err != nil {
		return nil, ErrUserNotFound
	}

	claimVersion := 1
	if tv, ok := claims["tokenVersion"].(float64); ok {
		claimVersion = int(tv)
	}
	if claimVersion != tokenVersion {
		return nil, ErrTokenExpired
	}

	if status == "banned" {
		return nil, ErrAccountBanned
	}
	// 队伍封禁只拦参赛选手；管理员仍需能登录后台处理善后
	if role == "user" && teamStatus == "banned" {
		return nil, ErrTeamBanned
	}

	return &Identity{UserID: userID, Role: role, Claims: claims}, nil
}

// HasRole 判断角色是否在允许列表内，allowed 为空表示不限角色
func HasRole(role string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if role == a {
			return true
		}
	}
	return false
}

// Bind 将身份写入请求上下文，键名与既有处理函数保持一致
func (id *Identity) Bind(c *gin.Context) {
	c.Set("claims", id.Claims)
	c.Set("role", id.Role)
	c.Set("userID", id.UserID)
}

// Abort 以统一格式返回鉴权错误并终止请求
func Abort(c *gin.Context, e *Error) {
	body := gin.H{"error": e.Code}
	if e.Message != "" {
		body["message"] = e.Message
	}
	c.JSON(e.Status, body)
	c.Abort()
}
