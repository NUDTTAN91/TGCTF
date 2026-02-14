-- Author: tan91
-- GitHub: https://github.com/NUDTTAN91
-- Blog: https://blog.csdn.net/ZXW_NUDT

-- 组织表（用于隔离不同比赛的用户和队伍）
CREATE TABLE IF NOT EXISTS organizations (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) UNIQUE NOT NULL,
    description TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'active', -- active | disabled
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_organizations_name ON organizations(name);
CREATE INDEX idx_organizations_status ON organizations(status);

-- 队伍表
CREATE TABLE IF NOT EXISTS teams (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) UNIQUE NOT NULL,
    description TEXT,
    captain_id INTEGER,                          -- 队长用户ID
    organization_id INTEGER REFERENCES organizations(id), -- 所属组织（管理员队伍可为空）
    is_admin_team BOOLEAN DEFAULT FALSE,         -- 是否为管理员队伍(TG_AdminX)
    avatar TEXT,                                  -- 队伍头像路径
    status VARCHAR(32) NOT NULL DEFAULT 'active', -- active | banned
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_teams_name ON teams(name);
CREATE INDEX idx_teams_captain ON teams(captain_id);
CREATE INDEX idx_teams_organization ON teams(organization_id);

CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(64) UNIQUE NOT NULL,
    display_name VARCHAR(128) NOT NULL,
    email VARCHAR(256),
    role VARCHAR(32) NOT NULL DEFAULT 'user',    -- user | admin | super
    status VARCHAR(32) NOT NULL DEFAULT 'active', -- active | banned
    password_hash TEXT NOT NULL,
    must_change_password BOOLEAN DEFAULT FALSE,   -- 导入用户首次登录需强制修改密码
    token_version INTEGER NOT NULL DEFAULT 1,     -- Token版本号，重置密码时递增以失效旧Token
    team_id INTEGER REFERENCES teams(id),
    organization_id INTEGER REFERENCES organizations(id), -- 所属组织（超管可为空）
    last_login_ip VARCHAR(64),
    last_login_at TIMESTAMP,
    avatar TEXT,                                   -- 头像图片路径
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 普通用户ID从101开始
ALTER SEQUENCE users_id_seq RESTART WITH 101;

CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_role ON users(role);
CREATE INDEX idx_users_status ON users(status);
CREATE INDEX idx_users_team_id ON users(team_id);
CREATE INDEX idx_users_organization ON users(organization_id);

-- 比赛表
CREATE TABLE IF NOT EXISTS contests (
    id SERIAL PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    description TEXT,
    mode VARCHAR(32) NOT NULL DEFAULT 'jeopardy',  -- jeopardy | awd
    status VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending | running | ended
    cover_image TEXT,  -- 背景图片URL
    team_limit INTEGER NOT NULL DEFAULT 4,         -- 队伍人数限制，0为不限制
    container_limit INTEGER NOT NULL DEFAULT 1,    -- 队伍容器限制，0为不限制
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP NOT NULL,
    first_blood_bonus INTEGER NOT NULL DEFAULT 5,  -- 一血奖励百分比
    second_blood_bonus INTEGER NOT NULL DEFAULT 3, -- 二血奖励百分比
    third_blood_bonus INTEGER NOT NULL DEFAULT 1,  -- 三血奖励百分比
    flag_format VARCHAR(128) DEFAULT 'flag{[GUID]}', -- Flag格式，[GUID]为占位符
    defense_interval INTEGER DEFAULT 300,             -- AWD-F 全局防守间隔（秒），默认5分钟
    judge_concurrency INTEGER DEFAULT 5,              -- AWD-F 并发判题数
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_contests_status ON contests(status);
CREATE INDEX idx_contests_start_time ON contests(start_time);

-- 比赛-组织关联表（多对多，一个比赛可有多个组织参与）
CREATE TABLE IF NOT EXISTS contest_organizations (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, organization_id)
);

CREATE INDEX idx_contest_orgs_contest ON contest_organizations(contest_id);
CREATE INDEX idx_contest_orgs_org ON contest_organizations(organization_id);

-- 题目类别表
CREATE TABLE IF NOT EXISTS categories (
    id SERIAL PRIMARY KEY,
    name VARCHAR(64) UNIQUE NOT NULL,       -- 类别名称: WEB, PWN, MISC 等
    icon_url TEXT,                          -- 类别图标URL (1:1比例)
    glow_color VARCHAR(32) DEFAULT '#ff6b00', -- 发光颜色 (十六进制或RGB)
    is_default BOOLEAN DEFAULT FALSE,       -- 是否为系统默认类别
    sort_order INTEGER DEFAULT 0,           -- 排序顺序
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_categories_name ON categories(name);

-- 题库表
CREATE TABLE IF NOT EXISTS question_bank (
    id SERIAL PRIMARY KEY,
    title VARCHAR(256) NOT NULL,
    type VARCHAR(32) NOT NULL,              -- static_attachment | static_container | dynamic_attachment | dynamic_container
    category_id INTEGER NOT NULL REFERENCES categories(id),
    difficulty INT NOT NULL DEFAULT 5,  -- 1-10 stars
    description TEXT,
    flag VARCHAR(512),                      -- 静态flag（动态题目可为空）
    flag_type VARCHAR(32) DEFAULT 'static', -- static | dynamic
    docker_image VARCHAR(256),              -- Docker镜像名（容器题目）
    attachment_url TEXT,                    -- 附件URL或本地路径
    attachment_type VARCHAR(16) DEFAULT 'url', -- 附件类型: url(外部链接) | local(本地上传)
    -- 镜像性能配置 (JSON格式存储)
    ports TEXT,                             -- JSON数组: ["80", "8080", "22"]
    cpu_limit VARCHAR(32),                  -- 如: "1.0", "0.5"
    memory_limit VARCHAR(32),               -- 如: "512m", "1g"
    storage_limit VARCHAR(32),              -- 如: "1g", "5g"
    no_resource_limit BOOLEAN DEFAULT FALSE,-- 是否不限制性能
    flag_env VARCHAR(64) DEFAULT 'FLAG',     -- Flag注入环境变量名 (FLAG, GZCTF_FLAG, CTF_FLAG 等)
    flag_script VARCHAR(256),                  -- Flag注入脚本路径 (如 /flag.sh，容器启动后执行)
    needs_edit BOOLEAN DEFAULT FALSE,        -- 是否需要再次编辑（Excel导入时标记有多端口/附件的题目）
    image_status VARCHAR(16),                 -- 镜像状态: exists | not_found | null(未测试)
    image_checked_at TIMESTAMP,               -- 镜像最后检查时间
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_question_bank_type ON question_bank(type);
CREATE INDEX idx_question_bank_category ON question_bank(category_id);
CREATE INDEX idx_question_bank_difficulty ON question_bank(difficulty);

-- 比赛题目关联表（从题库添加到比赛）
CREATE TABLE IF NOT EXISTS contest_challenges (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    question_id INTEGER NOT NULL REFERENCES question_bank(id) ON DELETE CASCADE,
    initial_score INTEGER NOT NULL DEFAULT 500,  -- 初始分数
    min_score INTEGER NOT NULL DEFAULT 100,      -- 最低分数
    difficulty INTEGER NOT NULL DEFAULT 5,       -- 难度系数（用于动态计分）
    display_order INTEGER DEFAULT 0,             -- 显示顺序
    hint TEXT DEFAULT '',                        -- 题目提示（每场比赛独立设置）
    hint_released BOOLEAN DEFAULT FALSE,         -- 提示是否已发布
    status VARCHAR(32) NOT NULL DEFAULT 'hidden',  -- hidden | public
    release_time TIMESTAMP,                      -- 题目开放时间
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, question_id)
);

CREATE INDEX idx_contest_challenges_contest ON contest_challenges(contest_id);
CREATE INDEX idx_contest_challenges_question ON contest_challenges(question_id);
CREATE INDEX idx_contest_challenges_status ON contest_challenges(status);

-- 题目提示表（支持每道题目多个提示）
CREATE TABLE IF NOT EXISTS contest_challenge_hints (
    id SERIAL PRIMARY KEY,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges(id) ON DELETE CASCADE,
    content TEXT NOT NULL,                       -- 提示内容
    released BOOLEAN DEFAULT FALSE,              -- 是否已发布
    released_at TIMESTAMP,                       -- 发布时间
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_challenge_hints_challenge ON contest_challenge_hints(challenge_id);
CREATE INDEX idx_challenge_hints_released ON contest_challenge_hints(released);

-- 旧的题目表（保留向后兼容，可逐步迁移）
CREATE TABLE IF NOT EXISTS challenges (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    name VARCHAR(256) NOT NULL,
    category VARCHAR(64) NOT NULL,  -- WEB, PWN, REVERSE, CRYPTO, MISC 等
    description TEXT,
    score INTEGER NOT NULL DEFAULT 500,
    flag VARCHAR(512) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'hidden',  -- hidden | public
    attachment_url TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_challenges_contest_id ON challenges(contest_id);
CREATE INDEX idx_challenges_category ON challenges(category);
CREATE INDEX idx_challenges_status ON challenges(status);

-- 初始化默认题目类别（带默认发光颜色）
INSERT INTO categories (name, glow_color, is_default, sort_order) VALUES 
    ('WEB', '#339AF0', TRUE, 1),
    ('MISC', '#20C997', TRUE, 2),
    ('PWN', '#FF6B6B', TRUE, 3),
    ('REVERSE', '#FCC419', TRUE, 4),
    ('CRYPTO', '#845EF7', TRUE, 5),
    ('OSINT', '#FF922B', TRUE, 6),
    ('OTHER', '#888888', TRUE, 7)
ON CONFLICT (name) DO NOTHING;

-- 队伍-题目-Flag 关联表（每队每题一个唯一flag）
CREATE TABLE IF NOT EXISTS team_challenge_flags (
    id SERIAL PRIMARY KEY,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL,                   -- 可能来自 contest_challenges 或 contest_challenges_awdf
    flag VARCHAR(512) NOT NULL,                      -- 该队伍该题目的唯一flag
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(team_id, challenge_id)                    -- 每队每题只能有一个flag
);

CREATE INDEX idx_team_challenge_flags_team ON team_challenge_flags(team_id);
CREATE INDEX idx_team_challenge_flags_challenge ON team_challenge_flags(challenge_id);

-- 队伍容器实例表（基于队伍，同队共享实例）
CREATE TABLE IF NOT EXISTS team_instances (
    id SERIAL PRIMARY KEY,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges(id) ON DELETE CASCADE,
    container_id VARCHAR(64) NOT NULL,            -- Docker容器ID
    container_name VARCHAR(128),                  -- 容器名称
    ports TEXT,                                   -- JSON: {"80": "32768", "8080": "32769"}
    status VARCHAR(32) NOT NULL DEFAULT 'running',  -- running | stopped | destroyed
    expires_at TIMESTAMP NOT NULL,                -- 过期时间
    created_by INTEGER REFERENCES users(id),      -- 创建者用户ID
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(team_id, challenge_id)                 -- 每队每题只能有一个实例
);

CREATE INDEX idx_team_instances_team ON team_instances(team_id);
CREATE INDEX idx_team_instances_contest ON team_instances(contest_id);
CREATE INDEX idx_team_instances_challenge ON team_instances(challenge_id);
CREATE INDEX idx_team_instances_status ON team_instances(status);
CREATE INDEX idx_team_instances_expires ON team_instances(expires_at);

-- 比赛-队伍关联表（队伍报名参赛及审核状态）
CREATE TABLE IF NOT EXISTS contest_teams (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending(待审核) | approved(已通过) | rejected(已拒绝)
    reviewed_by INTEGER REFERENCES users(id),       -- 审核人
    reviewed_at TIMESTAMP,                          -- 审核时间
    reject_reason TEXT,                             -- 拒绝原因
    allocated_ports INTEGER[] DEFAULT '{}',         -- AWD-F 模式预分配的端口数组
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, team_id)
);

CREATE INDEX idx_contest_teams_contest ON contest_teams(contest_id);
CREATE INDEX idx_contest_teams_team ON contest_teams(team_id);
CREATE INDEX idx_contest_teams_status ON contest_teams(status);

-- Flag提交记录表
CREATE TABLE IF NOT EXISTS submissions (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    flag VARCHAR(512) NOT NULL,                   -- 提交的flag
    is_correct BOOLEAN NOT NULL DEFAULT FALSE,    -- 是否正确
    is_cheating BOOLEAN NOT NULL DEFAULT FALSE,   -- 是否作弊
    cheating_victim_team_id INTEGER,              -- 被盗flag的队伍ID
    score INTEGER NOT NULL DEFAULT 0,             -- 获得的分数（正确时）
    ip_address VARCHAR(64),                       -- 提交时的IP地址
    submitted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_submissions_contest ON submissions(contest_id);
CREATE INDEX idx_submissions_challenge ON submissions(challenge_id);
CREATE INDEX idx_submissions_team ON submissions(team_id);
CREATE INDEX idx_submissions_user ON submissions(user_id);
CREATE INDEX idx_submissions_correct ON submissions(is_correct);

-- 队伍解题记录表（用于快速查询队伍已解题目）
CREATE TABLE IF NOT EXISTS team_solves (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    first_solver_id INTEGER REFERENCES users(id), -- 队内首个解题者
    solve_order INTEGER NOT NULL DEFAULT 0,       -- 解题顺序（1=一血,2=二血,3=三血...）用于动态计算分数
    solved_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, challenge_id, team_id)     -- 每队每题只能解一次
);

CREATE INDEX idx_team_solves_contest ON team_solves(contest_id);
CREATE INDEX idx_team_solves_challenge ON team_solves(challenge_id);
CREATE INDEX idx_team_solves_team ON team_solves(team_id);

-- 比赛公告表
CREATE TABLE IF NOT EXISTS contest_announcements (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    type VARCHAR(32) NOT NULL DEFAULT 'manual',   -- manual(手动) | challenge_open(题目开放) | challenge_close(题目下架) | first_blood(一血) | second_blood(二血) | third_blood(三血) | cheating(作弊)
    title VARCHAR(256) NOT NULL,
    content TEXT,
    is_pinned BOOLEAN DEFAULT FALSE,              -- 是否置顶
    created_by INTEGER REFERENCES users(id),      -- 发布者（手动公告）
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_announcements_contest ON contest_announcements(contest_id);
CREATE INDEX idx_announcements_type ON contest_announcements(type);
CREATE INDEX idx_announcements_created ON contest_announcements(created_at DESC);

-- 系统设置表（键值对存储）
CREATE TABLE IF NOT EXISTS system_settings (
    key VARCHAR(64) PRIMARY KEY,
    value TEXT NOT NULL,
    description TEXT,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 初始化默认系统设置
INSERT INTO system_settings (key, value, description) VALUES
    ('container_initial_ttl', '120', '容器初始有效期(分钟)'),
    ('container_extend_ttl', '120', '单次续期时长(分钟)'),
    ('container_extend_window', '15', '续期窗口(剩余分钟数)'),
    ('auto_destroy_expired', 'true', '自动销毁过期实例')
ON CONFLICT (key) DO NOTHING;

-- 队伍首次查看题目记录表（用于计算解题用时）
CREATE TABLE IF NOT EXISTS challenge_first_views (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL,                -- 可能来自 contest_challenges 或 contest_challenges_awdf
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- 首次查看的用户
    first_viewed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, challenge_id, team_id)  -- 每队每题只记录一次
);

CREATE INDEX idx_challenge_first_views_contest ON challenge_first_views(contest_id);
CREATE INDEX idx_challenge_first_views_challenge ON challenge_first_views(challenge_id);
CREATE INDEX idx_challenge_first_views_team ON challenge_first_views(team_id);

-- 系统日志表（记录所有操作）
CREATE TABLE IF NOT EXISTS system_logs (
    id SERIAL PRIMARY KEY,
    type VARCHAR(32) NOT NULL,                -- 日志类型: login | logout | container_create | container_destroy | container_extend | flag_submit | cheating | admin_op
    level VARCHAR(16) NOT NULL DEFAULT 'info', -- 日志级别: info | warning | error | success
    user_id INTEGER REFERENCES users(id),     -- 操作用户
    team_id INTEGER REFERENCES teams(id),     -- 关联队伍
    contest_id INTEGER REFERENCES contests(id), -- 关联比赛
    challenge_id INTEGER,                      -- 关联题目
    ip_address VARCHAR(64),                    -- 操作 IP
    message TEXT NOT NULL,                     -- 日志消息
    details JSONB,                             -- 额外详情（JSON格式）
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_system_logs_type ON system_logs(type);
CREATE INDEX idx_system_logs_level ON system_logs(level);
CREATE INDEX idx_system_logs_user ON system_logs(user_id);
CREATE INDEX idx_system_logs_contest ON system_logs(contest_id);
CREATE INDEX idx_system_logs_created ON system_logs(created_at DESC);

-- 大屏监控事件表（封禁、作弊、尝试解题等）
CREATE TABLE IF NOT EXISTS monitor_events (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    event_type VARCHAR(50) NOT NULL,          -- cheat | ban | attempt
    team_name VARCHAR(255) NOT NULL,
    user_name VARCHAR(255),                   -- 操作用户名（尝试解题时显示）
    challenge_name VARCHAR(255),              -- 可空（封禁事件无题目）
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_monitor_events_contest ON monitor_events(contest_id, created_at DESC);

-- 用户登录历史表（用于防作弊 IP 异常检测）
CREATE TABLE IF NOT EXISTS user_login_history (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address VARCHAR(64) NOT NULL,
    user_agent TEXT,
    login_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_user_login_history_user ON user_login_history(user_id);
CREATE INDEX idx_user_login_history_ip ON user_login_history(ip_address);
CREATE INDEX idx_user_login_history_time ON user_login_history(login_at DESC);

-- ============================================
-- AWD-F 模式相关表
-- ============================================

-- AWD-F 专属题库表（与普通题库隔离）
CREATE TABLE IF NOT EXISTS question_bank_awdf (
    id SERIAL PRIMARY KEY,
    title VARCHAR(256) NOT NULL,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    difficulty INT NOT NULL DEFAULT 5,            -- 1-10 难度
    description TEXT,                              -- 题目描述
    docker_image VARCHAR(256) NOT NULL,           -- Docker镜像名（AWD-F必须是容器题）
    -- 镜像性能配置
    ports TEXT,                                    -- JSON数组: ["80", "8080"]
    cpu_limit VARCHAR(32),                         -- 如: "1.0", "0.5"
    memory_limit VARCHAR(32),                      -- 如: "512m", "1g"
    storage_limit VARCHAR(32),                     -- 如: "1g", "5g"
    no_resource_limit BOOLEAN DEFAULT FALSE,       -- 是否不限制性能
    -- AWD-F 专属配置
    exp_script TEXT,                               -- EXP脚本内容（用于攻击验证）
    check_script TEXT,                             -- 功能检测脚本（验证服务是否正常）
    patch_whitelist TEXT,                          -- 允许修改的文件白名单 JSON: ["/var/www/html/index.php"]
    vulnerable_file TEXT,                          -- 漏洞文件路径（供出题人参考）
    flag_env VARCHAR(64) DEFAULT 'FLAG',           -- Flag注入环境变量名
    flag_script VARCHAR(256),                      -- Flag注入脚本路径
    -- 状态
    image_status VARCHAR(16),                      -- 镜像状态: exists | not_found | null
    image_checked_at TIMESTAMP,                    -- 镜像最后检查时间
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_question_bank_awdf_category ON question_bank_awdf(category_id);
CREATE INDEX idx_question_bank_awdf_difficulty ON question_bank_awdf(difficulty);

-- AWD-F 比赛题目关联表
CREATE TABLE IF NOT EXISTS contest_challenges_awdf (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    question_id INTEGER NOT NULL REFERENCES question_bank_awdf(id) ON DELETE CASCADE,
    initial_score INTEGER NOT NULL DEFAULT 500,    -- 初始分数（解题得分）
    min_score INTEGER NOT NULL DEFAULT 100,        -- 最低分数
    defense_score INTEGER NOT NULL DEFAULT 100,    -- 每轮防守成功得分
    attack_interval INTEGER NOT NULL DEFAULT 60,   -- 攻击间隔（秒）
    display_order INTEGER DEFAULT 0,               -- 显示顺序
    status VARCHAR(32) NOT NULL DEFAULT 'hidden',  -- hidden | public
    release_time TIMESTAMP,                        -- 题目开放时间
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, question_id)
);

CREATE INDEX idx_contest_challenges_awdf_contest ON contest_challenges_awdf(contest_id);
CREATE INDEX idx_contest_challenges_awdf_question ON contest_challenges_awdf(question_id);
CREATE INDEX idx_contest_challenges_awdf_status ON contest_challenges_awdf(status);

-- AWD-F 补丁记录表（选手上传的补丁）
CREATE TABLE IF NOT EXISTS awdf_patches (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges_awdf(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    patch_file TEXT NOT NULL,                      -- 补丁文件路径
    patch_hash VARCHAR(64),                        -- 补丁文件哈希（用于去重）
    status VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending | applied | rejected | failed
    reject_reason TEXT,                            -- 拒绝/失败原因
    applied_at TIMESTAMP,                          -- 应用时间
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_awdf_patches_contest ON awdf_patches(contest_id);
CREATE INDEX idx_awdf_patches_challenge ON awdf_patches(challenge_id);
CREATE INDEX idx_awdf_patches_team ON awdf_patches(team_id);
CREATE INDEX idx_awdf_patches_status ON awdf_patches(status);

-- AWD-F EXP执行结果表（每轮攻击记录）
CREATE TABLE IF NOT EXISTS awdf_exp_results (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges_awdf(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    round_number INTEGER NOT NULL,                 -- 轮次号
    exp_success BOOLEAN NOT NULL DEFAULT FALSE,    -- EXP是否攻击成功
    check_success BOOLEAN NOT NULL DEFAULT TRUE,   -- 功能检测是否通过
    defense_success BOOLEAN NOT NULL DEFAULT FALSE,-- 防守是否成功（EXP失败且检测通过）
    score_earned INTEGER NOT NULL DEFAULT 0,       -- 本轮获得的分数
    exp_output TEXT,                               -- EXP执行输出（调试用）
    check_output TEXT,                             -- 检测脚本输出
    executed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_awdf_exp_results_contest ON awdf_exp_results(contest_id);
CREATE INDEX idx_awdf_exp_results_challenge ON awdf_exp_results(challenge_id);
CREATE INDEX idx_awdf_exp_results_team ON awdf_exp_results(team_id);
CREATE INDEX idx_awdf_exp_results_round ON awdf_exp_results(round_number);

-- AWD-F 全局轮次记录表
CREATE TABLE IF NOT EXISTS awdf_rounds (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    round_number INTEGER NOT NULL,                    -- 轮次号
    started_at TIMESTAMP,                             -- 轮次开始时间
    completed_at TIMESTAMP,                           -- 轮次完成时间
    status VARCHAR(32) NOT NULL DEFAULT 'pending',   -- pending | running | completed
    teams_judged INTEGER DEFAULT 0,                   -- 已判题队伍数
    teams_total INTEGER DEFAULT 0,                    -- 总队伍数
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, round_number)
);

CREATE INDEX idx_awdf_rounds_contest ON awdf_rounds(contest_id);
CREATE INDEX idx_awdf_rounds_status ON awdf_rounds(status);

-- AWD-F 解题记录表（独立于普通 team_solves，避免外键约束冲突）
CREATE TABLE IF NOT EXISTS team_solves_awdf (
    id SERIAL PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges_awdf(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    first_solver_id INTEGER REFERENCES users(id),
    solve_order INTEGER NOT NULL DEFAULT 0,
    solved_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(contest_id, challenge_id, team_id)
);

CREATE INDEX idx_team_solves_awdf_contest ON team_solves_awdf(contest_id);
CREATE INDEX idx_team_solves_awdf_challenge ON team_solves_awdf(challenge_id);
CREATE INDEX idx_team_solves_awdf_team ON team_solves_awdf(team_id);

-- AWD-F 队伍容器实例表（独立于普通 team_instances，避免外键约束冲突）
CREATE TABLE IF NOT EXISTS team_instances_awdf (
    id SERIAL PRIMARY KEY,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    challenge_id INTEGER NOT NULL REFERENCES contest_challenges_awdf(id) ON DELETE CASCADE,
    container_id VARCHAR(64) NOT NULL,            -- Docker容器ID
    container_name VARCHAR(128),                  -- 容器名称
    ports TEXT,                                   -- JSON: {"80": "32768", "8080": "32769"}
    ssh_password VARCHAR(32),                     -- SSH登录密码（16位随机）
    status VARCHAR(32) NOT NULL DEFAULT 'running',  -- running | stopped | destroyed
    expires_at TIMESTAMP NOT NULL,                -- 过期时间
    created_by INTEGER REFERENCES users(id),      -- 创建者用户ID
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(team_id, challenge_id)                 -- 每队每题只能有一个实例
);

CREATE INDEX idx_team_instances_awdf_team ON team_instances_awdf(team_id);
CREATE INDEX idx_team_instances_awdf_contest ON team_instances_awdf(contest_id);
CREATE INDEX idx_team_instances_awdf_challenge ON team_instances_awdf(challenge_id);
CREATE INDEX idx_team_instances_awdf_status ON team_instances_awdf(status);
CREATE INDEX idx_team_instances_awdf_expires ON team_instances_awdf(expires_at);
  