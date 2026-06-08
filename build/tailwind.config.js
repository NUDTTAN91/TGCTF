/** Tailwind CSS 构建配置
 *
 * 用途：为仍依赖运行时编译的 16 个页面生成静态 CSS，产物为 web/assets/css/tailwind.css。
 * 其余 18 个 admin 页面使用作者早先预编译的 web/admin/css/style.css，不在本配置范围内。
 *
 * 本文件放在 build/ 而不是仓库根或 web/，原因：
 *   - web/ 下的文件会被静态托管直接对外提供（server/main.go 的 c.File("./web"+path)），
 *     构建配置不应该能被公开下载；
 *   - 也不会被打进运行镜像（Dockerfile 只 COPY web/）。
 *
 * 重新构建（必须在仓库根目录执行，content 路径按运行目录解析）：
 *   npx tailwindcss@3.4.17 -c build/tailwind.config.js -i build/tailwind.input.css \
 *     -o web/assets/css/tailwind.css --minify
 *
 * 无 Node 环境时用容器构建（同样在仓库根执行）：
 *   docker run --rm -v "$PWD":/app -w /app alpine:latest sh -c '
 *     sed -i "s|dl-cdn.alpinelinux.org|mirrors.tuna.tsinghua.edu.cn|g" /etc/apk/repositories
 *     apk add --no-cache nodejs npm
 *     npm config set registry https://registry.npmmirror.com
 *     cd /tmp && npm init -y && npm i tailwindcss@3.4.17 --no-audit --no-fund
 *     cd /app && /tmp/node_modules/.bin/tailwindcss -c build/tailwind.config.js \
 *       -i build/tailwind.input.css -o web/assets/css/tailwind.css --minify'
 *
 * 注意：新增页面若使用 Tailwind 类，需要把文件加入下面的 content 列表并重新构建，
 * 否则该页用到的工具类不会出现在产物中。
 */
module.exports = {
  content: [
    // 用户端页面
    './web/index.html',
    './web/login.html',
    './web/register.html',
    './web/home.html',
    './web/profile.html',
    './web/contests.html',
    './web/change-password.html',
    './web/contest-challenges.html',
    './web/contest-challenges-awdf.html',
    './web/contest-leaderboard.html',
    './web/contest-monitor.html',
    './web/contest-monitor-awdf.html',
    // 比赛题目编辑页（admin 与 portal 两套副本）
    './web/admin/admin-contest-edit-jeopardy.html',
    './web/admin/admin-contest-edit-awdf.html',
    './web/portal/admin-contest-edit-jeopardy.html',
    './web/portal/admin-contest-edit-awdf.html',
    // 公共脚本会动态生成带 Tailwind 类的侧边栏
    './web/assets/js/*.js',
  ],
  theme: { extend: {} },
  plugins: [],
};
