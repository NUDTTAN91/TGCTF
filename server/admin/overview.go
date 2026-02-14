// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package admin

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// OverviewStats 概览统计
type OverviewStats struct {
	Users      int `json:"users"`
	Contests   int `json:"contests"`
	Teams      int `json:"teams"`
	Challenges int `json:"challenges"`
	Docker     int `json:"docker"`
}

// HandleAdminOverview 后台概览统计
func HandleAdminOverview(c *gin.Context, db *sql.DB) {
	var stats OverviewStats

	// 查询用户数
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&stats.Users)

	// 查询比赛数
	db.QueryRow(`SELECT COUNT(*) FROM contests`).Scan(&stats.Contests)

	// 查询队伍数
	db.QueryRow(`SELECT COUNT(*) FROM teams`).Scan(&stats.Teams)

	// 查询题目数
	db.QueryRow(`SELECT COUNT(*) FROM challenges`).Scan(&stats.Challenges)

	// 查询Docker实例数
	db.QueryRow(`SELECT COUNT(*) FROM team_instances WHERE status = 'running'`).Scan(&stats.Docker)

	c.JSON(http.StatusOK, stats)
}
