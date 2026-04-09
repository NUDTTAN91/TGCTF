/**
 * TGCTF 管理员侧边栏动态注入脚本
 *
 * 功能：
 *   - 自动检测 /admin/ 或 /portal/ 路径前缀
 *   - 动态生成 13 项导航 + 分组标签
 *   - JWT 角色检测（super / admin），控制"管理员管理"可见性
 *   - 文件名匹配高亮（含聚合规则）
 *   - 底部固定：返回前台 + 登出
 *   - Font Awesome 动态加载
 *
 * 使用方式：在管理页面 <body> 末尾引入
 *   <script src="/assets/js/admin-sidebar.js"></script>
 */

(function () {
    'use strict';

    // ========== 工具函数 ==========

    /**
     * 解析 JWT Token，返回 payload 对象
     * @param {string} token
     * @returns {object|null}
     */
    function parseJwt(token) {
        try {
            var base64Url = token.split('.')[1];
            var base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
            var jsonPayload = decodeURIComponent(
                atob(base64).split('').map(function (c) {
                    return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
                }).join('')
            );
            return JSON.parse(jsonPayload);
        } catch (e) {
            return null;
        }
    }

    /**
     * 确保 Font Awesome 已加载，未加载则动态注入 CDN link
     */
    function ensureFontAwesome() {
        var existing = document.querySelector(
            'link[href*="font-awesome"], link[href*="fontawesome"]'
        );
        if (existing) return;
        var link = document.createElement('link');
        link.rel = 'stylesheet';
        link.href = 'https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css';
        document.head.appendChild(link);
    }

    // ========== 路径与角色检测 ==========

    /**
     * 检测当前 URL 路径前缀：/admin/ 或 /portal/
     * @returns {string} 如 "/admin/" 或 "/portal/"
     */
    function detectPrefix() {
        var path = window.location.pathname;
        if (path.indexOf('/portal/') === 0) return '/portal/';
        return '/admin/';
    }

    /**
     * 获取当前页面文件名（不含路径前缀）
     * 例如 /admin/admin-users.html → "admin-users.html"
     * @returns {string}
     */
    function getCurrentFileName() {
        var path = window.location.pathname;
        var idx = path.lastIndexOf('/');
        return idx >= 0 ? path.substring(idx + 1) : path;
    }

    /**
     * 根据文件名确定高亮的导航 key
     * 聚合规则：
     *   - admin-contest-edit-*.html → "admin-contests.html"
     *   - admin-questions*.html / admin-question-edit*.html → "admin-question-bank.html"
     *   - 其他直接匹配
     * @param {string} fileName
     * @returns {string} 匹配的导航 key
     */
    function resolveActiveKey(fileName) {
        // 比赛管理聚合
        if (fileName === 'admin-contest-edit-jeopardy.html' ||
            fileName === 'admin-contest-edit-awdf.html') {
            return 'admin-contests.html';
        }
        // 题库聚合
        if (fileName === 'admin-questions.html' ||
            fileName === 'admin-questions-awdf.html' ||
            fileName === 'admin-question-edit.html' ||
            fileName === 'admin-question-edit-awdf.html') {
            return 'admin-question-bank.html';
        }
        // 分类管理归属题库
        if (fileName === 'admin-categories.html') {
            return 'admin-question-bank.html';
        }
        return fileName;
    }

    // ========== 导航配置 ==========

    /**
     * 构建导航项定义
     * @param {string} prefix - 路径前缀
     * @returns {Array} 导航定义数组
     */
    function buildNavConfig(prefix) {
        return [
            // ---- 分组: 赛事核心 ----
            { type: 'group', label: '赛事核心' },
            { href: prefix + 'admin.html',              icon: 'fa-solid fa-chart-line',       label: '概览',       key: 'admin.html' },
            { href: prefix + 'admin-contests.html',     icon: 'fa-solid fa-trophy',           label: '比赛管理',   key: 'admin-contests.html' },
            { href: prefix + 'admin-question-bank.html',icon: 'fa-solid fa-database',         label: '题库',       key: 'admin-question-bank.html' },
            { href: prefix + 'admin-users.html',        icon: 'fa-solid fa-users',            label: '用户管理',   key: 'admin-users.html' },
            { href: prefix + 'admin-teams.html',        icon: 'fa-solid fa-people-group',     label: '队伍管理',   key: 'admin-teams.html' },
            { href: prefix + 'admin-organizations.html',icon: 'fa-solid fa-building',         label: '组织管理',   key: 'admin-organizations.html' },
            { href: prefix + 'admin-data-import.html',  icon: 'fa-solid fa-file-import',      label: '数据导入',   key: 'admin-data-import.html' },
            { href: prefix + 'admin-admins.html',       icon: 'fa-solid fa-user-shield',      label: '管理员管理', key: 'admin-admins.html', superOnly: true },

            // ---- 分组: 运维监控 ----
            { type: 'group', label: '运维监控' },
            { href: prefix + 'admin-docker.html',       icon: 'fa-brands fa-docker',          label: 'Docker实例', key: 'admin-docker.html' },
            { href: prefix + 'admin-anti-cheat.html',   icon: 'fa-solid fa-shield-halved',    label: '防作弊',     key: 'admin-anti-cheat.html' },
            { href: prefix + 'admin-logs.html',         icon: 'fa-solid fa-file-lines',       label: '系统日志',   key: 'admin-logs.html' },
            { href: prefix + 'admin-settings.html',     icon: 'fa-solid fa-gear',             label: '系统设置',   key: 'admin-settings.html' }
        ];
    }

    // ========== 侧边栏 HTML 构建 ==========

    /**
     * 生成管理员侧边栏完整 HTML
     * @param {string} activeKey - 当前高亮的导航 key
     * @param {string} prefix    - 路径前缀
     * @param {string} role      - 用户角色 ('super' | 'admin')
     * @returns {string} HTML 字符串
     */
    function buildAdminSidebarHTML(activeKey, prefix, role) {
        var navConfig = buildNavConfig(prefix);
        var html = '';

        // Mobile toggle
        html += '<button class="admin-mobile-toggle" id="admin-mobile-toggle" aria-label="Toggle admin sidebar">';
        html += '<i class="fa-solid fa-bars"></i></button>';

        // Overlay
        html += '<div class="admin-sidebar-overlay" id="admin-sidebar-overlay"></div>';

        // Sidebar
        html += '<aside class="admin-sidebar" id="admin-sidebar">';

        // Logo
        html += '<div class="admin-sidebar-logo">';
        html += '<div class="logo-icon">TG</div>';
        html += '<span class="logo-text">TGCTF</span>';
        html += '</div>';

        // Nav
        html += '<nav class="admin-sidebar-nav">';

        for (var i = 0; i < navConfig.length; i++) {
            var item = navConfig[i];

            // 分组标签
            if (item.type === 'group') {
                html += '<div class="admin-nav-group">' + item.label + '</div>';
                continue;
            }

            // super-only 项：非 super 角色隐藏
            var hideStyle = '';
            var superAttr = '';
            if (item.superOnly) {
                superAttr = ' data-super-only="true"';
                if (role !== 'super') {
                    hideStyle = ' style="display:none"';
                }
            }

            var activeCls = (item.key === activeKey) ? ' active' : '';
            html += '<a href="' + item.href + '" class="admin-nav-item' + activeCls + '"' +
                    superAttr + hideStyle + '>';
            html += '<i class="' + item.icon + '"></i>';
            html += '<span>' + item.label + '</span>';
            html += '</a>';
        }

        html += '</nav>';

        // Footer — 固定底部
        html += '<div class="admin-sidebar-footer">';
        html += '<a href="/home.html" class="admin-nav-item">';
        html += '<i class="fa-solid fa-arrow-left"></i><span>返回前台</span></a>';
        html += '<a href="javascript:void(0)" class="admin-nav-item" id="admin-sidebar-logout">';
        html += '<i class="fa-solid fa-right-from-bracket"></i><span>登出</span></a>';
        html += '</div>';

        html += '</aside>';

        return html;
    }

    // ========== 初始化 ==========

    function initAdminSidebar() {
        ensureFontAwesome();

        // 检测前缀与角色
        var prefix = detectPrefix();
        var role = 'admin';
        var token = localStorage.getItem('tg_token');
        if (token) {
            var payload = parseJwt(token);
            if (payload && payload.role) {
                role = payload.role;
            }
        }

        // 确定高亮项
        var fileName = getCurrentFileName();
        var activeKey = resolveActiveKey(fileName);

        // 注入 HTML
        var wrapper = document.createElement('div');
        wrapper.innerHTML = buildAdminSidebarHTML(activeKey, prefix, role);

        while (wrapper.firstChild) {
            document.body.insertBefore(wrapper.firstChild, document.body.firstChild);
        }

        // ---- 移动端切换 ----
        var toggleBtn = document.getElementById('admin-mobile-toggle');
        var overlay = document.getElementById('admin-sidebar-overlay');
        var sidebar = document.getElementById('admin-sidebar');

        function toggleAdminSidebar() {
            sidebar.classList.toggle('open');
            overlay.classList.toggle('show');
        }

        toggleBtn.addEventListener('click', toggleAdminSidebar);
        overlay.addEventListener('click', toggleAdminSidebar);

        // ---- 登出 ----
        var logoutBtn = document.getElementById('admin-sidebar-logout');
        logoutBtn.addEventListener('click', function () {
            localStorage.removeItem('tg_token');
            window.location.href = '/login.html';
        });
    }

    // 暴露到全局（兼容外部调用）
    window.toggleAdminSidebar = function () {
        var sidebar = document.getElementById('admin-sidebar');
        var overlay = document.getElementById('admin-sidebar-overlay');
        if (sidebar) sidebar.classList.toggle('open');
        if (overlay) overlay.classList.toggle('show');
    };

    // 页面加载时自动初始化
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initAdminSidebar);
    } else {
        initAdminSidebar();
    }
})();
