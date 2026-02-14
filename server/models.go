// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package main

// User 基本用户信息
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

// UserDetail 用户管理详细信息
type UserDetail struct {
	ID               int64   `json:"id"`
	Username         string  `json:"username"`
	DisplayName      string  `json:"displayName"`
	Email            *string `json:"email"`
	Role             string  `json:"role"`
	Status           string  `json:"status"`
	TeamID           *int64  `json:"teamId"`
	TeamName         *string `json:"teamName"`
	OrganizationID   *int64  `json:"organizationId"`
	OrganizationName *string `json:"organizationName"`
	LastLoginIP      *string `json:"lastLoginIp"`
	LastLoginAt      *string `json:"lastLoginAt"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

// Contest 比赛信息
type Contest struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Mode           string  `json:"mode"`           // jeopardy | awd
	Status         string  `json:"status"`         // pending | running | paused | ended
	CoverImage     *string `json:"coverImage"`     // 背景图URL
	TeamLimit      int     `json:"teamLimit"`      // 队伍人数限制
	ContainerLimit int     `json:"containerLimit"` // 队伍容器限制
	StartTime      string  `json:"startTime"`
	EndTime        string  `json:"endTime"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	TeamsCount     int64   `json:"teamsCount,omitempty"`
}

// Challenge 题目信息
type Challenge struct {
	ID            int64   `json:"id"`
	ContestID     int64   `json:"contestId"`
	Name          string  `json:"name"`
	Category      string  `json:"category"`  // WEB, PWN, REVERSE, CRYPTO, MISC
	Type          string  `json:"type"`      // static_attachment | static_container | dynamic_attachment | dynamic_container
	Description   string  `json:"description"`
	Score         int     `json:"score"`
	Flag          string  `json:"flag,omitempty"` // 只有管理员可见
	Status        string  `json:"status"`         // hidden | public
	AttachmentURL *string `json:"attachmentUrl"`
	DockerImage   *string `json:"dockerImage,omitempty"` // Docker镜像
	Ports         *string `json:"ports,omitempty"`       // 端口配置
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

// 请求类型
type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type createContestRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Mode        string `json:"mode" binding:"required"`
	CoverImage  string `json:"coverImage"`
	StartTime   string `json:"startTime" binding:"required"`
	EndTime     string `json:"endTime" binding:"required"`
}

type updateContestRequest struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	CoverImage     string `json:"coverImage"`
	TeamLimit      *int   `json:"teamLimit"`
	ContainerLimit *int   `json:"containerLimit"`
	StartTime      string `json:"startTime"`
	EndTime        string `json:"endTime"`
}

type createUserRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"displayName" binding:"required"`
	Email       string `json:"email"`
	Password    string `json:"password" binding:"required"`
	Role        string `json:"role"`
}

type updateUserRequest struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

type resetPasswordRequest struct {
	NewPassword string `json:"newPassword" binding:"required"`
}

type createChallengeRequest struct {
	Name          string `json:"name" binding:"required"`
	Category      string `json:"category" binding:"required"`
	Description   string `json:"description"`
	Score         int    `json:"score"`
	Flag          string `json:"flag" binding:"required"`
	Status        string `json:"status"`
	AttachmentURL string `json:"attachmentUrl"`
}

type updateChallengeRequest struct {
	Name          string `json:"name"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Score         int    `json:"score"`
	Flag          string `json:"flag"`
	Status        string `json:"status"`
	AttachmentURL string `json:"attachmentUrl"`
}

// overviewStats 概览统计
type overviewStats struct {
	Contests   int64 `json:"contests"`
	Users      int64 `json:"users"`
	Teams      int64 `json:"teams"`
	Challenges int64 `json:"challenges"`
	Docker     int64 `json:"docker"`
}

// ========== 题目类别 ==========

// Category 题目类别
type Category struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	IconURL   *string `json:"iconUrl"`
	GlowColor *string `json:"glowColor"` // 发光颜色(十六进制或RGB)
	IsDefault bool    `json:"isDefault"`
	SortOrder int     `json:"sortOrder"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

type createCategoryRequest struct {
	Name      string `json:"name" binding:"required"`
	IconURL   string `json:"iconUrl"`
	GlowColor string `json:"glowColor"` // 发光颜色
	SortOrder int    `json:"sortOrder"`
}

type updateCategoryRequest struct {
	Name      string `json:"name"`
	IconURL   string `json:"iconUrl"`
	GlowColor string `json:"glowColor"` // 发光颜色
	SortOrder int    `json:"sortOrder"`
}

// ========== 题库 ==========

// QuestionBank 题库题目
type QuestionBank struct {
	ID              int64    `json:"id"`
	Title           string   `json:"title"`
	Type            string   `json:"type"`            // static_attachment | static_container | dynamic_attachment | dynamic_container
	CategoryID      int64    `json:"categoryId"`
	CategoryName    string   `json:"categoryName,omitempty"` // 关联查询
	Difficulty      int      `json:"difficulty"`      // 1-10 stars
	Description     string   `json:"description"`
	Flag            *string  `json:"flag,omitempty"` // 静态flag（仅管理员可见）
	FlagType        string   `json:"flagType"`       // static | dynamic
	DockerImage     *string  `json:"dockerImage"`    // Docker镜像名
	AttachmentURL   *string  `json:"attachmentUrl"` // 附件URL或本地路径
	AttachmentType  string   `json:"attachmentType"` // 附件类型: url | local
	Ports           *string  `json:"ports"`          // JSON数组: ["80", "8080"]
	CPULimit        *string  `json:"cpuLimit"`
	MemoryLimit     *string  `json:"memoryLimit"`
	StorageLimit    *string  `json:"storageLimit"`
	NoResourceLimit bool     `json:"noResourceLimit"`
	FlagEnv         *string  `json:"flagEnv"`        // Flag注入环境变量名
	NeedsEdit       bool     `json:"needsEdit"`      // 是否需要再次编辑
	ImageStatus     *string  `json:"imageStatus"`    // 镜像状态: exists | not_found
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

type createQuestionRequest struct {
	Title           string `json:"title" binding:"required"`
	Type            string `json:"type" binding:"required"` // static_attachment | static_container | dynamic_attachment | dynamic_container
	CategoryID      int64  `json:"categoryId" binding:"required"`
	Difficulty      int    `json:"difficulty"`
	Description     string `json:"description"`
	Flag            string `json:"flag"`
	FlagType        string `json:"flagType"`        // static | dynamic
	DockerImage     string `json:"dockerImage"`
	AttachmentURL   string `json:"attachmentUrl"`
	AttachmentType  string `json:"attachmentType"` // url | local
	Ports           string `json:"ports"`            // JSON数组字符串
	CPULimit        string `json:"cpuLimit"`
	MemoryLimit     string `json:"memoryLimit"`
	StorageLimit    string `json:"storageLimit"`
	NoResourceLimit bool   `json:"noResourceLimit"`
	FlagEnv         string `json:"flagEnv"` // Flag注入环境变量名
}

type updateQuestionRequest struct {
	Title           string `json:"title"`
	Type            string `json:"type"`
	CategoryID      int64  `json:"categoryId"`
	Difficulty      int    `json:"difficulty"`
	Description     string `json:"description"`
	Flag            string `json:"flag"`
	FlagType        string `json:"flagType"`
	DockerImage     string `json:"dockerImage"`
	AttachmentURL   string `json:"attachmentUrl"`
	AttachmentType  string `json:"attachmentType"`
	Ports           string `json:"ports"`
	CPULimit        string `json:"cpuLimit"`
	MemoryLimit     string `json:"memoryLimit"`
	StorageLimit    string `json:"storageLimit"`
	NoResourceLimit bool   `json:"noResourceLimit"`
	FlagEnv         string `json:"flagEnv"`
}

// ========== 比赛题目关联 ==========

// ContestChallenge 比赛题目关联
type ContestChallenge struct {
	ID           int64   `json:"id"`
	ContestID    int64   `json:"contestId"`
	QuestionID   int64   `json:"questionId"`
	Question     *QuestionBank `json:"question,omitempty"` // 关联的题库题目
	InitialScore int     `json:"initialScore"`
	MinScore     int     `json:"minScore"`
	Status       string  `json:"status"`      // hidden | public
	ReleaseTime  *string `json:"releaseTime"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type addContestChallengeRequest struct {
	QuestionID   int64  `json:"questionId" binding:"required"`
	InitialScore int    `json:"initialScore"`
	MinScore     int    `json:"minScore"`
	Status       string `json:"status"`
	ReleaseTime  string `json:"releaseTime"`
}

type updateContestChallengeRequest struct {
	InitialScore int    `json:"initialScore"`
	MinScore     int    `json:"minScore"`
	Status       string `json:"status"`
	ReleaseTime  string `json:"releaseTime"`
}
