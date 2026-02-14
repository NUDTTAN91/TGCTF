// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package docker

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GetOrCreateTeamFlag 获取或创建队伍的flag
func GetOrCreateTeamFlag(db *sql.DB, teamID int64, contestID, challengeID string) string {
	var flag string
	err := db.QueryRow(`SELECT flag FROM team_challenge_flags WHERE team_id = $1 AND challenge_id = $2`,
		teamID, challengeID).Scan(&flag)
	if err == nil {
		return flag
	}

	// 获取比赛的flag格式
	var flagFormat sql.NullString
	db.QueryRow(`SELECT flag_format FROM contests WHERE id = $1`, contestID).Scan(&flagFormat)
	format := "flag{[GUID]}"
	if flagFormat.Valid && flagFormat.String != "" {
		format = flagFormat.String
	}

	flag = GenerateFlag(format)
	db.Exec(`INSERT INTO team_challenge_flags (team_id, contest_id, challenge_id, flag) VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id, challenge_id) DO NOTHING`,
		teamID, contestID, challengeID, flag)
	return flag
}

// GenerateFlagsForTeamInContest 为某个队伍生成比赛中所有公开题目的flag
func GenerateFlagsForTeamInContest(db *sql.DB, contestID string, teamID int64, mode string) {
	// 获取比赛模式（如果 mode 为空，从数据库查询）
	contestMode := mode
	if contestMode == "" {
		db.QueryRow(`SELECT mode FROM contests WHERE id = $1`, contestID).Scan(&contestMode)
	}

	var rows *sql.Rows
	var err error
	if contestMode == "awd-f" {
		rows, err = db.Query(`SELECT id FROM contest_challenges_awdf WHERE contest_id = $1 AND status = 'public'`, contestID)
	} else {
		rows, err = db.Query(`SELECT id FROM contest_challenges WHERE contest_id = $1 AND status = 'public'`, contestID)
	}
	if err != nil {
		log.Printf("query public challenges error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var challengeID int64
		if err := rows.Scan(&challengeID); err != nil {
			continue
		}
		GetOrCreateTeamFlag(db, teamID, contestID, fmt.Sprintf("%d", challengeID))
	}
	log.Printf("Generated flags for team %d in contest %s", teamID, contestID)
}

// GenerateFlagsForChallengeInContest 为某个题目生成所有已通过审核且属于参与组织的队伍的flag
func GenerateFlagsForChallengeInContest(db *sql.DB, contestID string, challengeID string) {
	// 检查比赛是否设置了参与组织
	var orgCount int
	db.QueryRow(`SELECT COUNT(*) FROM contest_organizations WHERE contest_id = $1`, contestID).Scan(&orgCount)
	hasOrgLimit := orgCount > 0

	var query string
	if hasOrgLimit {
		// 有组织限制：只为属于参与组织的队伍生成
		query = `
			SELECT ct.team_id FROM contest_teams ct
			JOIN teams t ON ct.team_id = t.id
			WHERE ct.contest_id = $1 AND ct.status = 'approved'
			AND t.organization_id IN (SELECT organization_id FROM contest_organizations WHERE contest_id = $1)`
	} else {
		// 无组织限制：为所有已审核队伍生成
		query = `SELECT team_id FROM contest_teams WHERE contest_id = $1 AND status = 'approved'`
	}

	rows, err := db.Query(query, contestID)
	if err != nil {
		log.Printf("query approved teams error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var teamID int64
		if err := rows.Scan(&teamID); err != nil {
			continue
		}
		GetOrCreateTeamFlag(db, teamID, contestID, challengeID)
	}
	log.Printf("Generated flags for challenge %s in contest %s", challengeID, contestID)
}

// GenerateFlag 根据格式生成flag
// format 支持 [GUID] 占位符，例如: "flag{[GUID]}" -> "flag{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}"
func GenerateFlag(format string) string {
	uuid := fmt.Sprintf("%08x-%04x-4%03x-%04x-%012x",
		time.Now().UnixNano()&0xffffffff,
		time.Now().UnixNano()>>32&0xffff,
		time.Now().UnixNano()>>48&0x0fff,
		0x8000|(time.Now().UnixNano()>>60&0x3fff),
		time.Now().UnixNano())
	
	if format == "" {
		format = "flag{[GUID]}"
	}
	
	// 替换占位符
	result := strings.Replace(format, "[GUID]", uuid, 1)
	return result
}

// HandleGetChallengeFlags 获取比赛题目的所有队伍Flag列表
func HandleGetChallengeFlags(c *gin.Context, db *sql.DB) {
	challengeID := c.Param("id")
	// 从URL参数获取比赛模式，用于区分 jeopardy 和 awdf 表
	contestMode := c.Query("mode")

	// 先获取该题目对应的比赛ID（根据模式从对应表查询）
	var contestID string
	var err error
	if contestMode == "awd-f" {
		// AWD-F 模式：只从 awdf 表查询
		err = db.QueryRow(`SELECT contest_id FROM contest_challenges_awdf WHERE id = $1`, challengeID).Scan(&contestID)
	} else {
		// 默认 jeopardy 模式
		err = db.QueryRow(`SELECT contest_id FROM contest_challenges WHERE id = $1`, challengeID).Scan(&contestID)
	}
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CHALLENGE_NOT_FOUND"})
		return
	}

	// 检查比赛是否设置了参与组织
	var hasOrgLimit bool
	var orgCount int
	db.QueryRow(`SELECT COUNT(*) FROM contest_organizations WHERE contest_id = $1`, contestID).Scan(&orgCount)
	hasOrgLimit = orgCount > 0

	// 为所有已审核通过且属于参与组织的队伍生成Flag（如果还没有）
	var genQuery string
	if hasOrgLimit {
		// 有组织限制：只为属于参与组织的队伍生成
		genQuery = `
			SELECT ct.team_id FROM contest_teams ct
			JOIN teams t ON ct.team_id = t.id
			WHERE ct.contest_id = $1 AND ct.status = 'approved'
			AND t.organization_id IN (SELECT organization_id FROM contest_organizations WHERE contest_id = $1)
			AND NOT EXISTS (
				SELECT 1 FROM team_challenge_flags tcf 
				WHERE tcf.team_id = ct.team_id AND tcf.challenge_id = $2 AND tcf.contest_id = $1
			)`
	} else {
		// 无组织限制：为所有已审核队伍生成
		genQuery = `
			SELECT ct.team_id FROM contest_teams ct
			WHERE ct.contest_id = $1 AND ct.status = 'approved'
			AND NOT EXISTS (
				SELECT 1 FROM team_challenge_flags tcf 
				WHERE tcf.team_id = ct.team_id AND tcf.challenge_id = $2 AND tcf.contest_id = $1
			)`
	}
	rows, _ := db.Query(genQuery, contestID, challengeID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var teamID int64
			if err := rows.Scan(&teamID); err == nil {
				GetOrCreateTeamFlag(db, teamID, contestID, challengeID)
			}
		}
	}

	// 构建查询：只查询该比赛已通过审核且属于参与组织的队伍的Flag（必须匹配 contest_id）
	var query string
	if hasOrgLimit {
		// 有组织限制：只显示属于参与组织的队伍
		query = `
			SELECT tcf.id, tcf.team_id, t.name as team_name, tcf.flag, tcf.created_at
			FROM team_challenge_flags tcf
			JOIN teams t ON tcf.team_id = t.id
			JOIN contest_teams ct ON ct.team_id = tcf.team_id AND ct.contest_id = tcf.contest_id
			WHERE tcf.challenge_id = $1 AND tcf.contest_id = $2 AND ct.status = 'approved'
			AND t.organization_id IN (SELECT organization_id FROM contest_organizations WHERE contest_id = $2)
			ORDER BY t.name`
	} else {
		// 无组织限制：显示所有已审核队伍
		query = `
			SELECT tcf.id, tcf.team_id, t.name as team_name, tcf.flag, tcf.created_at
			FROM team_challenge_flags tcf
			JOIN teams t ON tcf.team_id = t.id
			JOIN contest_teams ct ON ct.team_id = tcf.team_id AND ct.contest_id = tcf.contest_id
			WHERE tcf.challenge_id = $1 AND tcf.contest_id = $2 AND ct.status = 'approved'
			ORDER BY t.name`
	}
	rows, err = db.Query(query, challengeID, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
		return
	}
	defer rows.Close()

	type TeamFlag struct {
		ID        int64  `json:"id"`
		TeamID    int64  `json:"teamId"`
		TeamName  string `json:"teamName"`
		Flag      string `json:"flag"`
		CreatedAt string `json:"createdAt"`
	}

	var flags []TeamFlag
	for rows.Next() {
		var f TeamFlag
		var createdAt sql.NullTime
		if err := rows.Scan(&f.ID, &f.TeamID, &f.TeamName, &f.Flag, &createdAt); err != nil {
			continue
		}
		if createdAt.Valid {
			f.CreatedAt = createdAt.Time.Format("2006-01-02 15:04:05")
		}
		flags = append(flags, f)
	}

	if flags == nil {
		flags = []TeamFlag{}
	}

	c.JSON(http.StatusOK, flags)
}
