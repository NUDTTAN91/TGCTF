// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package main

import (
	"database/sql"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"

	"tgctf/server/admin"
	"tgctf/server/awdf"
	"tgctf/server/contest"
	"tgctf/server/docker"
	dataimport "tgctf/server/import"
	"tgctf/server/logs"
	"tgctf/server/monitor"
	"tgctf/server/question"
	"tgctf/server/submission"
	"tgctf/server/user"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL not set")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	if err := ensureAdmin(db); err != nil {
		log.Fatalf("failed to ensure admin user: %v", err)
	}

	// 初始化Flag生成函数（用于队伍审核通过时自动生成Flag）
	contest.GenerateFlagsForTeamInContest = docker.GenerateFlagsForTeamInContest

	// 初始化自动公告函数（用于Flag提交时自动发布一二三血、作弊公告）
	submission.AnnounceBlood = contest.AnnounceBlood
	submission.AnnounceCheating = contest.AnnounceCheating

	// 初始化题目状态变更公告函数
	question.AnnounceChallenge = contest.AnnounceChallenge

	// 初始化题目放题时的Flag生成函数
	question.GenerateTeamChallengeFlag = docker.GenerateFlagsForChallengeInContest

	// 初始化AWD-F题目放题时的Flag生成函数
	awdf.GenerateTeamChallengeFlag = docker.GenerateFlagsForChallengeInContest

	// 初始化容器设置获取函数
	docker.GetContainerTTL = admin.GetContainerTTL
	docker.GetContainerExtendTTL = admin.GetContainerExtendTTL
	docker.GetContainerExtendWindow = admin.GetContainerExtendWindow

	// 初始化端口分配函数
	docker.AllocatePorts = admin.AllocatePorts

	// 初始化 AWD-F 比赛状态变更钩子
	contest.OnAWDFContestStatusChange = awdf.HandleContestStatusChange
	contest.AllocatePortsFunc = admin.AllocatePorts
	awdf.AllocatePortsFunc = admin.AllocatePorts
	awdf.GetContainerTTLFunc = admin.GetContainerTTL

	// 初始化 AWD-F 大屏事件记录函数
	awdf.AddMonitorEventFunc = monitor.AddMonitorEventToDB
	awdf.AddPatchEventFunc = monitor.AddMonitorEventToDB
	awdf.AddContainerEventFunc = monitor.AddMonitorEventToDB

	// 初始化 AWD-F 排行榜广播函数
	awdf.BroadcastRankingsFunc = func(db *sql.DB, contestID string) {
		data := monitor.GetMonitorDataForBroadcast(db, contestID)
		monitor.BroadcastMonitorUpdate(contestID, data)
	}

	r := gin.Default()

	api := r.Group("/api")
	{
		api.POST("/login", func(c *gin.Context) {
			handleLogin(c, db, []byte(jwtSecret))
		})

		// 公开的比赛列表API（无需认证）
		api.GET("/contests", func(c *gin.Context) {
			contest.HandlePublicContests(c, db)
		})

		// 大屏WebSocket实时推送（不经过中间件，自己验证token）
		api.GET("/contests/:id/monitor/ws", func(c *gin.Context) {
			monitor.HandleMonitorWebSocket(c, []byte(jwtSecret))
		})

		// ========== 公开的排行榜和大屏API（无需认证）==========
		api.GET("/contests/:id/scoreboard", func(c *gin.Context) {
			submission.HandleGetScoreboard(c, db)
		})
		api.GET("/contests/:id/scoreboard/solo", func(c *gin.Context) {
			submission.HandleGetSoloScoreboard(c, db)
		})
		api.GET("/contests/:id/scoreboard/trend", func(c *gin.Context) {
			submission.HandleGetScoreTrend(c, db)
		})
		api.GET("/contests/:id/solves/recent", func(c *gin.Context) {
			monitor.HandleGetRecentSolves(c, db)
		})
		api.GET("/contests/:id/monitor", func(c *gin.Context) {
			monitor.HandleGetMonitorData(c, db)
		})

		// 需要登录的用户API
		userAPI := api.Group("")
		userAPI.Use(userAuthMiddleware([]byte(jwtSecret), db))
		{
			// 获取比赛详情（登录用户）
			userAPI.GET("/contests/:id", func(c *gin.Context) {
				contest.HandleGetContest(c, db)
			})
			// 获取比赛题目列表（登录用户，只返回公开题目，不返回flag）
			userAPI.GET("/contests/:id/challenges", func(c *gin.Context) {
				question.HandlePublicChallenges(c, db)
			})

			// ========== 用户容器实例 API ==========
			userAPI.POST("/contests/:id/challenges/:challengeId/instance", func(c *gin.Context) {
				docker.HandleCreateUserInstance(c, db)
			})
			userAPI.GET("/contests/:id/challenges/:challengeId/instance", func(c *gin.Context) {
				docker.HandleGetUserInstance(c, db)
			})
			userAPI.DELETE("/contests/:id/challenges/:challengeId/instance", func(c *gin.Context) {
				docker.HandleDestroyUserInstance(c, db)
			})
			userAPI.POST("/contests/:id/challenges/:challengeId/instance/extend", func(c *gin.Context) {
				docker.HandleExtendUserInstance(c, db)
			})

			// 检查用户队伍在比赛中的审核状态
			userAPI.GET("/contests/:id/team-status", func(c *gin.Context) {
				contest.HandleCheckTeamStatus(c, db)
			})

			// ========== Flag提交与解题 API ==========
			userAPI.POST("/contests/:id/challenges/:challengeId/submit", func(c *gin.Context) {
				submission.HandleSubmitFlag(c, db)
			})
			userAPI.GET("/contests/:id/solves", func(c *gin.Context) {
				submission.HandleGetTeamSolves(c, db)
			})
			userAPI.GET("/contests/:id/challenges/:challengeId/stats", func(c *gin.Context) {
				submission.HandleGetChallengeStats(c, db)
			})
			// 批量获取所有题目血量统计（不记录首次查看）
			userAPI.GET("/contests/:id/challenges/blood", func(c *gin.Context) {
				submission.HandleGetAllChallengesBlood(c, db)
			})
			// 比赛公告
			userAPI.GET("/contests/:id/announcements", func(c *gin.Context) {
				contest.HandleListAnnouncements(c, db)
			})

			// 获取题目已发布的提示（用户端）
			userAPI.GET("/contests/:id/challenges/:challengeId/hints", func(c *gin.Context) {
				question.HandleGetReleasedHints(c, db)
			})

			// ========== AWD-F 补丁上传（选手端） ==========
			userAPI.POST("/contests/:id/challenges/:challengeId/patch", func(c *gin.Context) {
				awdf.HandleUploadPatch(c, db)
			})
			userAPI.GET("/contests/:id/challenges/:challengeId/patches", func(c *gin.Context) {
				awdf.HandleListTeamPatches(c, db)
			})
			userAPI.GET("/patches/:patchId", func(c *gin.Context) {
				awdf.HandleGetPatchStatus(c, db)
			})
			// AWD-F 重置容器（选手端）
			userAPI.POST("/contests/:id/challenges/:challengeId/reset", func(c *gin.Context) {
				awdf.HandleResetContainer(db, c)
			})
			// AWD-F 获取全局攻击倒计时（选手端）
			userAPI.GET("/contests/:id/awdf/countdown", func(c *gin.Context) {
				contestIDStr := c.Param("id")
				contestID, _ := strconv.ParseInt(contestIDStr, 10, 64)
				status := awdf.GetSchedulerStatus(contestID)
				c.JSON(200, status)
			})

			// 类别列表（公开API，用于获取颜色配置）
			userAPI.GET("/categories", func(c *gin.Context) {
				question.HandleListCategories(c, db)
			})

			// ========== 用户个人中心 API ==========
			userAPI.GET("/profile", func(c *gin.Context) {
				user.HandleGetProfile(c, db)
			})
			userAPI.PUT("/profile", func(c *gin.Context) {
				user.HandleUpdateProfile(c, db)
			})
			userAPI.POST("/profile/password", func(c *gin.Context) {
				user.HandleChangePassword(c, db)
			})
			userAPI.GET("/profile/team", func(c *gin.Context) {
				user.HandleGetMyTeam(c, db)
			})
			userAPI.POST("/profile/avatar", func(c *gin.Context) {
				user.HandleUploadAvatar(c, db)
			})
			userAPI.POST("/team/avatar", func(c *gin.Context) {
				user.HandleUploadTeamAvatar(c, db)
			})
			userAPI.POST("/logout", func(c *gin.Context) {
				user.HandleLogout(c, db)
			})
		}

		// 超级管理员后台API
		adminAPI := api.Group("/admin")
		adminAPI.Use(authMiddleware([]byte(jwtSecret)))
		{
			adminAPI.GET("/overview", func(c *gin.Context) {
				admin.HandleAdminOverview(c, db)
			})

			// 比赛管理 CRUD
			adminAPI.GET("/contests", func(c *gin.Context) {
				contest.HandleListContests(c, db)
			})
			adminAPI.POST("/contests", func(c *gin.Context) {
				contest.HandleCreateContest(c, db)
			})
			adminAPI.GET("/contests/:id", func(c *gin.Context) {
				contest.HandleGetContest(c, db)
			})
			adminAPI.PUT("/contests/:id", func(c *gin.Context) {
				contest.HandleUpdateContest(c, db)
			})
			adminAPI.DELETE("/contests/:id", func(c *gin.Context) {
				contest.HandleDeleteContest(c, db)
			})
			// 三血奖励配置
			adminAPI.GET("/contests/:id/bonus", func(c *gin.Context) {
				contest.HandleGetBonusConfig(c, db)
			})
			adminAPI.PUT("/contests/:id/bonus", func(c *gin.Context) {
				contest.HandleUpdateBonusConfig(c, db)
			})

			// 用户管理 CRUD
			adminAPI.GET("/users", func(c *gin.Context) {
				admin.HandleListUsers(c, db)
			})
			adminAPI.POST("/users", func(c *gin.Context) {
				admin.HandleCreateUser(c, db)
			})
			adminAPI.GET("/users/:id", func(c *gin.Context) {
				admin.HandleGetUser(c, db)
			})
			adminAPI.PUT("/users/:id", func(c *gin.Context) {
				admin.HandleUpdateUser(c, db)
			})
			adminAPI.DELETE("/users/:id", func(c *gin.Context) {
				admin.HandleDeleteUser(c, db)
			})
			adminAPI.POST("/users/:id/reset-password", func(c *gin.Context) {
				admin.HandleResetPassword(c, db)
			})
			// 批量用户操作
			adminAPI.POST("/users/batch-ban", func(c *gin.Context) {
				admin.HandleBatchBanUsers(c, db)
			})
			adminAPI.POST("/users/batch-unban", func(c *gin.Context) {
				admin.HandleBatchUnbanUsers(c, db)
			})
			adminAPI.POST("/users/batch-reset-password", func(c *gin.Context) {
				admin.HandleBatchResetPasswords(c, db)
			})
			// 设置用户队伍
			adminAPI.PUT("/users/:id/team", func(c *gin.Context) {
				admin.HandleSetUserTeam(c, db)
			})
			// 获取所有队伍列表（用于下拉选择）
			adminAPI.GET("/all-teams", func(c *gin.Context) {
				admin.HandleGetAllTeams(c, db)
			})

			// 题目管理 CRUD (嵌套在比赛下 - 旧版，保留兼容)
			adminAPI.GET("/contests/:id/challenges", func(c *gin.Context) {
				question.HandleListChallenges(c, db)
			})
			adminAPI.POST("/contests/:id/challenges", func(c *gin.Context) {
				question.HandleCreateChallenge(c, db)
			})
			adminAPI.GET("/challenges/:id", func(c *gin.Context) {
				question.HandleGetChallenge(c, db)
			})
			adminAPI.PUT("/challenges/:id", func(c *gin.Context) {
				question.HandleUpdateChallenge(c, db)
			})
			adminAPI.DELETE("/challenges/:id", func(c *gin.Context) {
				question.HandleDeleteChallenge(c, db)
			})

			// ========== 题目类别管理 ==========
			adminAPI.GET("/categories", func(c *gin.Context) {
				question.HandleListCategories(c, db)
			})
			adminAPI.POST("/categories", func(c *gin.Context) {
				question.HandleCreateCategory(c, db)
			})
			adminAPI.GET("/categories/:id", func(c *gin.Context) {
				question.HandleGetCategory(c, db)
			})
			adminAPI.PUT("/categories/:id", func(c *gin.Context) {
				question.HandleUpdateCategory(c, db)
			})
			adminAPI.DELETE("/categories/:id", func(c *gin.Context) {
				question.HandleDeleteCategory(c, db)
			})

			// ========== 题库管理 ==========
			adminAPI.GET("/questions", func(c *gin.Context) {
				question.HandleListQuestions(c, db)
			})
			adminAPI.POST("/questions", func(c *gin.Context) {
				question.HandleCreateQuestion(c, db)
			})
			adminAPI.GET("/questions/:id", func(c *gin.Context) {
				question.HandleGetQuestion(c, db)
			})
			adminAPI.PUT("/questions/:id", func(c *gin.Context) {
				question.HandleUpdateQuestion(c, db)
			})
			adminAPI.DELETE("/questions/:id", func(c *gin.Context) {
				question.HandleDeleteQuestion(c, db)
			})
			// 题库批量导入
			adminAPI.POST("/questions/import", func(c *gin.Context) {
				question.HandleImportQuestions(c, db)
			})
			// 题库导入模板下载
			adminAPI.GET("/questions/template", func(c *gin.Context) {
				question.HandleDownloadQuestionTemplate(c, db)
			})
			// 批量镜像测试
			adminAPI.POST("/questions/batch-test-images", func(c *gin.Context) {
				question.HandleBatchTestImages(c, db)
			})

			// ========== 附件管理 ==========
			adminAPI.POST("/attachments/upload", func(c *gin.Context) {
				question.HandleUploadAttachment(c, db)
			})
			adminAPI.DELETE("/attachments/:filename", func(c *gin.Context) {
				question.HandleDeleteAttachment(c, db)
			})

			// ========== 比赛题目关联（新版，从题库添加） ==========
			adminAPI.GET("/contests/:id/contest-challenges", func(c *gin.Context) {
				question.HandleListContestChallenges(c, db)
			})
			adminAPI.POST("/contests/:id/contest-challenges", func(c *gin.Context) {
				question.HandleAddContestChallenge(c, db)
			})
			adminAPI.PUT("/contest-challenges/:id", func(c *gin.Context) {
				question.HandleUpdateContestChallenge(c, db)
			})
			adminAPI.DELETE("/contest-challenges/:id", func(c *gin.Context) {
				question.HandleRemoveContestChallenge(c, db)
			})
			// 批量更新题目显示顺序
			adminAPI.PUT("/contests/:id/contest-challenges/order", func(c *gin.Context) {
				question.HandleBatchUpdateChallengeOrder(c, db)
			})
			// 更新题目提示
			adminAPI.PUT("/contest-challenges/:id/hint", func(c *gin.Context) {
				question.HandleUpdateChallengeHint(c, db)
			})
			// 发布题目提示
			adminAPI.POST("/contest-challenges/:id/hint/release", func(c *gin.Context) {
				question.HandleReleaseChallengeHint(c, db)
			})
			// 多提示支持 - 获取题目所有提示
			adminAPI.GET("/contest-challenges/:id/hints", func(c *gin.Context) {
				question.HandleListChallengeHints(c, db)
			})
			// 多提示支持 - 添加新提示
			adminAPI.POST("/contest-challenges/:id/hints", func(c *gin.Context) {
				question.HandleAddChallengeHint(c, db)
			})
			// 多提示支持 - 删除提示
			adminAPI.DELETE("/contest-challenges/:id/hints/:hintId", func(c *gin.Context) {
				question.HandleDeleteChallengeHint(c, db)
			})
			// 多提示支持 - 发布单个提示
			adminAPI.POST("/contest-challenges/:id/hints/:hintId/release", func(c *gin.Context) {
				question.HandleReleaseSingleHint(c, db)
			})
			// 获取比赛题目的所有队伍Flag
			adminAPI.GET("/contest-challenges/:id/flags", func(c *gin.Context) {
				docker.HandleGetChallengeFlags(c, db)
			})

			// ========== 队伍管理 ==========
			adminAPI.GET("/teams", func(c *gin.Context) {
				admin.HandleListTeams(c, db)
			})
			adminAPI.POST("/teams", func(c *gin.Context) {
				admin.HandleCreateTeam(c, db)
			})
			adminAPI.GET("/teams/:id", func(c *gin.Context) {
				admin.HandleGetTeam(c, db)
			})
			adminAPI.PUT("/teams/:id", func(c *gin.Context) {
				admin.HandleUpdateTeam(c, db)
			})
			adminAPI.DELETE("/teams/:id", func(c *gin.Context) {
				admin.HandleDeleteTeam(c, db)
			})
			adminAPI.GET("/teams/:id/members", func(c *gin.Context) {
				admin.HandleGetTeamMembers(c, db)
			})
			adminAPI.POST("/teams/:id/members", func(c *gin.Context) {
				admin.HandleAddTeamMember(c, db)
			})
			adminAPI.DELETE("/teams/:id/members/:userId", func(c *gin.Context) {
				admin.HandleRemoveTeamMember(c, db)
			})
			adminAPI.POST("/teams/:id/captain/:userId", func(c *gin.Context) {
				admin.HandleSetTeamCaptain(c, db)
			})
			adminAPI.GET("/users-without-team", func(c *gin.Context) {
				admin.HandleGetUsersWithoutTeam(c, db)
			})

			// ========== 组织管理 ==========
			adminAPI.GET("/organizations", func(c *gin.Context) {
				admin.HandleListOrganizations(c, db)
			})
			adminAPI.POST("/organizations", func(c *gin.Context) {
				admin.HandleCreateOrganization(c, db)
			})
			adminAPI.GET("/organizations/:id", func(c *gin.Context) {
				admin.HandleGetOrganization(c, db)
			})
			adminAPI.PUT("/organizations/:id", func(c *gin.Context) {
				admin.HandleUpdateOrganization(c, db)
			})
			adminAPI.DELETE("/organizations/:id", func(c *gin.Context) {
				admin.HandleDeleteOrganization(c, db)
			})
			adminAPI.GET("/organizations/:id/check-captains", func(c *gin.Context) {
				admin.HandleCheckOrganizationCaptains(c, db)
			})
			adminAPI.GET("/organizations/:id/users", func(c *gin.Context) {
				admin.HandleGetOrganizationUsers(c, db)
			})
			adminAPI.GET("/organizations/:id/teams", func(c *gin.Context) {
				admin.HandleGetOrganizationTeams(c, db)
			})
			adminAPI.POST("/organizations/:id/users", func(c *gin.Context) {
				admin.HandleAddUserToOrganization(c, db)
			})
			adminAPI.DELETE("/organizations/:id/users/:userId", func(c *gin.Context) {
				admin.HandleRemoveUserFromOrganization(c, db)
			})
			adminAPI.POST("/organizations/:id/teams", func(c *gin.Context) {
				admin.HandleAddTeamToOrganization(c, db)
			})
			adminAPI.DELETE("/organizations/:id/teams/:teamId", func(c *gin.Context) {
				admin.HandleRemoveTeamFromOrganization(c, db)
			})
			adminAPI.GET("/users-without-organization", func(c *gin.Context) {
				admin.HandleGetUsersWithoutOrganization(c, db)
			})
			adminAPI.GET("/teams-without-organization", func(c *gin.Context) {
				admin.HandleGetTeamsWithoutOrganization(c, db)
			})
			adminAPI.GET("/contests/:id/organizations", func(c *gin.Context) {
				admin.HandleGetContestOrganizations(c, db)
			})
			adminAPI.PUT("/contests/:id/organizations", func(c *gin.Context) {
				admin.HandleSetContestOrganizations(c, db)
			})

			// ========== 比赛队伍审核 ==========
			adminAPI.GET("/contests/:id/teams", func(c *gin.Context) {
				contest.HandleGetContestTeams(c, db)
			})
			adminAPI.GET("/contests/:id/teams/ws", func(c *gin.Context) {
				contest.HandleAuditWebSocket(c)
			})
			adminAPI.POST("/contests/:id/teams/:teamId/review", func(c *gin.Context) {
				contest.HandleReviewContestTeam(c, db)
			})
			adminAPI.POST("/contests/:id/teams/batch-review", func(c *gin.Context) {
				contest.HandleBatchReviewContestTeams(c, db)
			})
			adminAPI.POST("/contests/:id/allocate-ports", func(c *gin.Context) {
				contest.HandleAllocatePorts(c, db)
			})

			// ========== 比赛公告管理 ==========
			adminAPI.GET("/contests/:id/announcements", func(c *gin.Context) {
				contest.HandleListAnnouncements(c, db)
			})
			adminAPI.POST("/contests/:id/announcements", func(c *gin.Context) {
				contest.HandleCreateAnnouncement(c, db)
			})
			adminAPI.PUT("/contests/:id/announcements/:announcementId", func(c *gin.Context) {
				contest.HandleUpdateAnnouncement(c, db)
			})
			adminAPI.DELETE("/contests/:id/announcements/:announcementId", func(c *gin.Context) {
				contest.HandleDeleteAnnouncement(c, db)
			})

			// ========== 数据导入 ==========
			adminAPI.POST("/import/users", func(c *gin.Context) {
				dataimport.HandleImportUsersJSON(c, db)
			})
			adminAPI.POST("/import/users/excel", func(c *gin.Context) {
				dataimport.HandleImportUsersExcel(c, db)
			})
			adminAPI.POST("/import/users/preview", func(c *gin.Context) {
				dataimport.HandlePreviewImportUsers(c, db)
			})
			adminAPI.GET("/import/template", func(c *gin.Context) {
				dataimport.HandleDownloadImportTemplate(c, db)
			})
			adminAPI.GET("/import/stats", func(c *gin.Context) {
				dataimport.HandleGetImportStats(c, db)
			})

			// ========== Docker镜像测试和容器管理 ==========
			adminAPI.POST("/docker/check-image", func(c *gin.Context) {
				docker.HandleCheckDockerImage(c, db)
			})
			adminAPI.POST("/docker/pull-image", func(c *gin.Context) {
				docker.HandlePullDockerImage(c, db)
			})
			adminAPI.POST("/docker/test-container", func(c *gin.Context) {
				docker.HandleCreateTestContainer(c, db)
			})
			adminAPI.GET("/docker/test-containers", func(c *gin.Context) {
				docker.HandleListTestContainers(c, db)
			})
			adminAPI.POST("/docker/test-container/:id/stop", func(c *gin.Context) {
				docker.HandleStopTestContainer(c, db)
			})
			adminAPI.DELETE("/docker/test-container/:id", func(c *gin.Context) {
				docker.HandleRemoveTestContainer(c, db)
			})

			// ========== Docker实例管理（管理员） ==========
			adminAPI.GET("/docker/instances", func(c *gin.Context) {
				docker.HandleAdminListInstances(c, db)
			})
			adminAPI.GET("/docker/instances/stats", func(c *gin.Context) {
				docker.HandleAdminGetStats(c, db)
			})
			adminAPI.GET("/docker/instances/contests", func(c *gin.Context) {
				docker.HandleAdminGetContestsForDocker(c, db)
			})
			adminAPI.DELETE("/docker/instances/:instanceId", func(c *gin.Context) {
				docker.HandleAdminDestroyInstance(c, db)
			})
			adminAPI.POST("/docker/instances/clean", func(c *gin.Context) {
				docker.HandleAdminCleanExpired(c, db)
			})
			adminAPI.POST("/docker/instances/batch-destroy", func(c *gin.Context) {
				docker.HandleAdminBatchDestroy(c, db)
			})
			adminAPI.GET("/docker/instances/:instanceId/logs", func(c *gin.Context) {
				docker.HandleAdminGetContainerLogs(c, db)
			})

			// ========== 系统设置 ==========
			adminAPI.GET("/settings", func(c *gin.Context) {
				admin.HandleGetSystemSettings(c, db)
			})
			adminAPI.PUT("/settings", func(c *gin.Context) {
				admin.HandleUpdateSystemSettings(c, db)
			})

			// ========== 系统日志 ==========
			adminAPI.GET("/logs", func(c *gin.Context) {
				logs.HandleGetLogs(c, db)
			})
			adminAPI.GET("/logs/ws", func(c *gin.Context) {
				logs.HandleLogsWebSocket(c)
			})

			// ========== 防作弊系统 ==========
			adminAPI.GET("/anti-cheat/suspicious", func(c *gin.Context) {
				admin.HandleGetSuspiciousRecords(c, db)
			})
			adminAPI.GET("/anti-cheat/ip-analysis", func(c *gin.Context) {
				admin.HandleGetIPAnalysis(c, db)
			})
			adminAPI.GET("/anti-cheat/team/:teamId/ip-track", func(c *gin.Context) {
				admin.HandleGetTeamIPTrack(c, db)
			})
			adminAPI.GET("/anti-cheat/banned-teams", func(c *gin.Context) {
				admin.HandleGetCheatingBannedTeams(c, db)
			})
			adminAPI.POST("/anti-cheat/ban", func(c *gin.Context) {
				admin.HandleBanTeamForCheating(c, db)
			})
			adminAPI.POST("/anti-cheat/unban", func(c *gin.Context) {
				admin.HandleUnbanTeam(c, db)
			})
			adminAPI.GET("/anti-cheat/ws", func(c *gin.Context) {
				admin.HandleAntiCheatWebSocket(c)
			})

			// ========== AWD-F 题库管理 ==========
			adminAPI.GET("/awdf/questions", func(c *gin.Context) {
				awdf.HandleListAWDFQuestions(c, db)
			})
			adminAPI.POST("/awdf/questions", func(c *gin.Context) {
				awdf.HandleCreateAWDFQuestion(c, db)
			})
			adminAPI.GET("/awdf/questions/:id", func(c *gin.Context) {
				awdf.HandleGetAWDFQuestion(c, db)
			})
			adminAPI.PUT("/awdf/questions/:id", func(c *gin.Context) {
				awdf.HandleUpdateAWDFQuestion(c, db)
			})
			adminAPI.DELETE("/awdf/questions/:id", func(c *gin.Context) {
				awdf.HandleDeleteAWDFQuestion(c, db)
			})
			adminAPI.GET("/awdf/stats", func(c *gin.Context) {
				awdf.HandleGetAWDFStats(c, db)
			})

			// ========== AWD-F 比赛题目关联 ==========
			adminAPI.GET("/contests/:id/awdf-challenges", func(c *gin.Context) {
				awdf.HandleListAWDFContestChallenges(c, db)
			})
			adminAPI.POST("/contests/:id/awdf-challenges", func(c *gin.Context) {
				awdf.HandleAddAWDFContestChallenge(c, db)
			})
			adminAPI.PUT("/awdf-challenges/:id", func(c *gin.Context) {
				awdf.HandleUpdateAWDFContestChallenge(c, db)
			})
			adminAPI.DELETE("/awdf-challenges/:id", func(c *gin.Context) {
				awdf.HandleRemoveAWDFContestChallenge(c, db)
			})

			// ========== AWD-F 补丁管理（管理员） ==========
			adminAPI.GET("/contests/:id/patches", func(c *gin.Context) {
				awdf.HandleAdminListPatches(c, db)
			})

			// ========== AWD-F EXP执行（管理员） ==========
			adminAPI.GET("/contests/:id/exp-results", func(c *gin.Context) {
				awdf.HandleGetExpResults(c, db)
			})
			adminAPI.POST("/contests/:id/exp/run", func(c *gin.Context) {
				awdf.HandleManualRunExp(c, db)
			})
			adminAPI.GET("/contests/:id/defense-stats", func(c *gin.Context) {
				awdf.HandleGetTeamDefenseStats(c, db)
			})
			// AWD-F 手动触发下一轮攻击
			adminAPI.POST("/contests/:id/awdf/trigger-next-round", func(c *gin.Context) {
				contestIDStr := c.Param("id")
				contestID, _ := strconv.ParseInt(contestIDStr, 10, 64)
				if awdf.TriggerNextRound(contestID) {
					c.JSON(200, gin.H{"success": true, "message": "已触发下一轮攻击"})
				} else {
					c.JSON(400, gin.H{"error": "触发失败，调度器未运行或已有触发等待中"})
				}
			})
			// AWD-F 获取调度器状态
			adminAPI.GET("/contests/:id/awdf/scheduler-status", func(c *gin.Context) {
				contestIDStr := c.Param("id")
				contestID, _ := strconv.ParseInt(contestIDStr, 10, 64)
				status := awdf.GetSchedulerStatus(contestID)
				c.JSON(200, status)
			})
			// AWD-F 更新防守间隔
			adminAPI.PUT("/contests/:id/awdf/defense-interval", func(c *gin.Context) {
				contestIDStr := c.Param("id")
				contestID, _ := strconv.ParseInt(contestIDStr, 10, 64)
				var req struct {
					Interval int `json:"interval"`
				}
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(400, gin.H{"error": "参数错误"})
					return
				}
				// 更新数据库
				db.Exec(`UPDATE contests SET defense_interval = $1 WHERE id = $2`, req.Interval, contestID)
				// 更新运行中的调度器
				awdf.UpdateDefenseInterval(contestID, req.Interval)
				c.JSON(200, gin.H{"success": true, "message": "防守间隔已更新"})
			})
		}
	}

	// 静态页面托管
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/" {
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
			c.File("./web/index.html")
			return
		}
		// 附件下载：/attachments/xxx
		if len(path) > 13 && path[:13] == "/attachments/" {
			serveAttachments(c)
			return
		}
		// HTML文件禁用缓存
		if len(path) > 5 && path[len(path)-5:] == ".html" {
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}
		c.File("./web" + path)
	})

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	// 启动过期容器自动清理任务
	admin.StartCleanupScheduler(db)
	log.Println("已启动过期容器自动清理任务")

	// 启动比赛状态自动更新定时器（触发AWD-F容器创建）
	contest.StartContestStatusUpdater(db)

	// 启动定时放题检查器
	question.StartScheduledReleaseChecker(db)
	log.Println("已启动定时放题检查器")

	// 启动AWD-F攻击调度器
	awdf.CheckAndStartSchedulers(db)
	log.Println("已启动AWD-F攻击调度器")

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

// serveAttachments 处理附件下载
func serveAttachments(c *gin.Context) {
	path := c.Request.URL.Path
	filename := path[13:] // 去掉 /attachments/ 前缀
	filePath := "./attachments/" + filename
	c.File(filePath)
}
