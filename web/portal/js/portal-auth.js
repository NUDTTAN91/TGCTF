// portal-auth.js - 普通管理员权限检查公共脚本

(function() {
    const token = localStorage.getItem('tg_token');
    
    function parseJwt(t) {
        try {
            const base64Url = t.split('.')[1];
            const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
            return JSON.parse(decodeURIComponent(atob(base64).split('').map(c => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2)).join('')));
        } catch (e) { return null; }
    }

    // 检查登录状态
    if (!token) {
        window.location.href = '/login.html';
        return;
    }

    const payload = parseJwt(token);
    if (!payload || (payload.role !== 'super' && payload.role !== 'admin')) {
        window.location.href = '/login.html';
        return;
    }

    // 超级管理员跳转到 admin 目录
    if (payload.role === 'super') {
        const currentPath = window.location.pathname;
        const newPath = currentPath.replace('/portal/', '/admin/');
        window.location.href = newPath;
        return;
    }

    // 定义每个页面需要的权限（普通管理员暂时只有数据大屏权限）
    // 未来可以扩展更多权限类型
    const pagePermissions = {
        'admin.html': 'none', // 首页不需要特殊权限
        'admin-contests.html': 'contest.menu.view', // 比赛管理需要比赛权限
        'admin-admins.html': 'super_only', // 仅超管
        'admin-users.html': 'super_only',
        'admin-teams.html': 'super_only',
        'admin-organizations.html': 'super_only',
        'admin-data-import.html': 'super_only',
        'admin-docker.html': 'super_only',
        'admin-anti-cheat.html': 'super_only',
        'admin-logs.html': 'super_only',
        'admin-settings.html': 'super_only',
        'admin-categories.html': 'super_only',
        'admin-question-bank.html': 'super_only',
        'admin-questions.html': 'super_only',
        'admin-questions-awdf.html': 'super_only',
        'admin-question-edit.html': 'super_only',
        'admin-question-edit-awdf.html': 'super_only',
        'admin-contest-edit-jeopardy.html': 'contest.edit', // 比赛编辑需要比赛权限
        'admin-contest-edit-awdf.html': 'contest.edit',
        'admin-flag-env.html': 'super_only'
    };

    // 获取当前页面文件名
    const currentPage = window.location.pathname.split('/').pop();
    const requiredPermission = pagePermissions[currentPage];

    // 暴露给页面使用的变量
    window.portalAuth = {
        token: token,
        payload: payload,
        role: payload.role,
        hasPermission: true,
        checkPermission: checkPermission
    };

    // 如果需要超管权限，显示无操作权限
    if (requiredPermission === 'super_only') {
        window.portalAuth.hasPermission = false;
        // 等 DOM 加载完成后替换内容
        document.addEventListener('DOMContentLoaded', function() {
            showNoPermission('此功能仅超级管理员可用');
        });
    }

    // 检查具体权限（用于页面内动态检查）
    async function checkPermission(permissionPattern) {
        try {
            const res = await fetch('/api/admin-common/my-permissions', {
                headers: { 'Authorization': 'Bearer ' + token }
            });
            if (!res.ok) return false;
            const data = await res.json();
            const permissions = data.permissions || [];
            
            // 检查是否有匹配的权限
            return permissions.some(p => {
                if (permissionPattern instanceof RegExp) {
                    return permissionPattern.test(p.permission);
                }
                return p.permission === permissionPattern;
            });
        } catch (e) {
            console.error('权限检查失败:', e);
            return false;
        }
    }

    // 显示无操作权限提示
    function showNoPermission(message) {
        const main = document.querySelector('main');
        if (main) {
            main.innerHTML = `
                <div class="flex-1 flex items-center justify-center" style="min-height: 60vh;">
                    <div class="text-center">
                        <div style="font-size: 96px; margin-bottom: 24px; opacity: 0.3;">🔒</div>
                        <div style="font-size: 24px; font-weight: bold; color: white; margin-bottom: 8px;">无操作权限</div>
                        <div style="font-size: 14px; color: #888;">${message || '您没有访问此功能的权限'}</div>
                        <a href="/portal/admin.html" style="display: inline-block; margin-top: 24px; padding: 10px 24px; background: #ff6b00; color: black; font-weight: bold; font-size: 14px; text-decoration: none;">返回首页</a>
                    </div>
                </div>
            `;
        }
    }

    // 暴露 showNoPermission 函数
    window.portalAuth.showNoPermission = showNoPermission;
})();
