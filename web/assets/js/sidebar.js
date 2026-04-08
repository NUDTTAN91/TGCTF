/**
 * TGCTF 共享侧边栏交互脚本
 * 使用方式：在页面 <body> 末尾引入 <script src="/assets/js/sidebar.js"></script>
 */

(function () {
    'use strict';

    // ========== 工具函数 ==========

    /** 解析 JWT Token */
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

    /** 确保 Font Awesome 已加载 */
    function ensureFontAwesome() {
        var existing = document.querySelector('link[href*="font-awesome"], link[href*="fontawesome"]');
        if (existing) return;
        var link = document.createElement('link');
        link.rel = 'stylesheet';
        link.href = 'https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css';
        document.head.appendChild(link);
    }

    // ========== 路径匹配 ==========

    function getActivePath() {
        var path = window.location.pathname;
        if (path === '/' || path === '/home.html') return 'home';
        if (path === '/contests.html' ||
            path.indexOf('/contest-challenges') === 0 ||
            path === '/contest-leaderboard.html' ||
            path.indexOf('/contest-monitor') === 0) return 'contests';
        if (path === '/profile.html') return 'profile';
        if (path.indexOf('/admin/') === 0 || path.indexOf('/portal/') === 0) return 'admin';
        return '';
    }

    // ========== 侧边栏 HTML ==========

    function buildSidebarHTML(activeKey) {
        function navItem(href, icon, label, key, extra) {
            var cls = 'nav-item' + (key === activeKey ? ' active' : '');
            var attrs = extra || '';
            return '<a href="' + href + '" class="' + cls + '" ' + attrs + '>' +
                '<i class="fas ' + icon + '"></i><span>' + label + '</span></a>';
        }

        var nav = '';
        nav += navItem('/home.html', 'fa-home', '首页', 'home');
        nav += navItem('/contests.html', 'fa-trophy', '赛事中心', 'contests');
        nav += navItem('/profile.html', 'fa-user', '个人中心', 'profile');
        nav += navItem('/admin/admin.html', 'fa-shield-alt', '管理后台', 'admin',
            'id="sidebar-admin-entry" style="display:none"');

        var html = '';

        // Mobile toggle
        html += '<button class="mobile-toggle" id="sidebar-mobile-toggle" aria-label="Toggle sidebar">' +
            '<i class="fas fa-bars"></i></button>';

        // Overlay
        html += '<div class="sidebar-overlay" id="sidebar-overlay"></div>';

        // Sidebar
        html += '<aside class="sidebar" id="sidebar">';
        html += '<div class="sidebar-logo">';
        html += '<div class="logo-icon">TG</div>';
        html += '<span class="logo-text">TGCTF</span>';
        html += '</div>';
        html += '<nav class="sidebar-nav">' + nav + '<div class="nav-divider"></div></nav>';
        html += '<div class="sidebar-footer">';
        html += '<a class="nav-item" id="sidebar-logout" href="javascript:void(0)">' +
            '<i class="fas fa-sign-out-alt"></i><span>登出</span></a>';
        html += '</div>';
        html += '</aside>';

        return html;
    }

    // ========== 初始化 ==========

    function initSidebar() {
        ensureFontAwesome();

        var activeKey = getActivePath();
        var wrapper = document.createElement('div');
        wrapper.innerHTML = buildSidebarHTML(activeKey);

        // 将生成的元素逐个插入 body 最前面
        while (wrapper.firstChild) {
            document.body.insertBefore(wrapper.firstChild, document.body.firstChild);
        }

        // ---- 移动端切换 ----
        var toggleBtn = document.getElementById('sidebar-mobile-toggle');
        var overlay = document.getElementById('sidebar-overlay');
        var sidebar = document.getElementById('sidebar');

        function toggleSidebar() {
            sidebar.classList.toggle('open');
            overlay.classList.toggle('show');
        }

        toggleBtn.addEventListener('click', toggleSidebar);
        overlay.addEventListener('click', toggleSidebar);

        // ---- 管理员入口 ----
        var token = localStorage.getItem('tg_token');
        if (token) {
            var payload = parseJwt(token);
            if (payload && (payload.role === 'super' || payload.role === 'admin')) {
                var adminEntry = document.getElementById('sidebar-admin-entry');
                if (adminEntry) {
                    adminEntry.style.display = '';
                    // 根据角色设置不同入口
                    adminEntry.href = payload.role === 'super' ? '/admin/admin.html' : '/portal/admin.html';
                }
            }
        }

        // ---- 登出 ----
        var logoutBtn = document.getElementById('sidebar-logout');
        logoutBtn.addEventListener('click', function () {
            localStorage.removeItem('tg_token');
            window.location.href = '/login.html';
        });
    }

    // 暴露 toggleSidebar 到全局（兼容可能的外部调用）
    window.toggleSidebar = function () {
        var sidebar = document.getElementById('sidebar');
        var overlay = document.getElementById('sidebar-overlay');
        if (sidebar) sidebar.classList.toggle('open');
        if (overlay) overlay.classList.toggle('show');
    };

    // 页面加载时自动初始化
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initSidebar);
    } else {
        initSidebar();
    }
})();
