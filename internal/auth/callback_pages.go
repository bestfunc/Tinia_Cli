// OAuth 回调页面 HTML —— 浏览器授权完跳回 127.0.0.1:<port> 时显示。
// 不依赖外部资源（CDN / 字体），离线也能渲染。

package auth

import (
	"fmt"
	"html"
)

const callbackBaseStyle = `
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  html, body { margin: 0; height: 100%; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", "Helvetica Neue", sans-serif;
    background: radial-gradient(ellipse at 50% -10%, #1e1b4b 0%, #0b0b13 55%, #050509 100%);
    color: #e5e7eb;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    min-height: 100vh;
  }
  .card {
    max-width: 440px;
    width: 100%;
    padding: 44px 36px 32px;
    border-radius: 20px;
    background: rgba(17, 18, 28, 0.72);
    border: 1px solid rgba(167, 139, 250, 0.18);
    text-align: center;
    backdrop-filter: blur(14px);
    -webkit-backdrop-filter: blur(14px);
    box-shadow:
      0 24px 72px rgba(0, 0, 0, 0.55),
      inset 0 1px 0 rgba(255, 255, 255, 0.04);
  }
  .icon {
    width: 68px; height: 68px;
    margin: 0 auto 22px;
    border-radius: 50%;
    display: flex; align-items: center; justify-content: center;
  }
  .icon svg { width: 34px; height: 34px; stroke: #fff; fill: none; stroke-width: 3; stroke-linecap: round; stroke-linejoin: round; }
  h1 { margin: 0 0 10px; font-size: 22px; font-weight: 600; letter-spacing: 0.4px; }
  p { margin: 0; color: #94a3b8; font-size: 14px; line-height: 1.75; }
  .reason {
    margin: 18px 0 0;
    padding: 10px 14px;
    border-radius: 10px;
    background: rgba(244, 63, 94, 0.08);
    border: 1px solid rgba(244, 63, 94, 0.22);
    color: #fca5a5;
    font-size: 12px;
    line-height: 1.6;
    word-break: break-word;
    text-align: left;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  }
  .brand {
    margin-top: 30px;
    padding-top: 22px;
    border-top: 1px solid rgba(255, 255, 255, 0.05);
    font-size: 10px;
    letter-spacing: 5px;
    color: #475569;
    text-transform: uppercase;
  }
  .brand strong { color: #a78bfa; font-weight: 600; }
</style>
`

const successHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<title>Tinia · 授权成功</title>` + callbackBaseStyle + `
<style>
  .icon { background: linear-gradient(135deg, #a78bfa, #6366f1); box-shadow: 0 0 44px rgba(167, 139, 250, 0.5); }
</style>
</head>
<body>
  <main class="card" role="status" aria-live="polite">
    <div class="icon" aria-hidden="true">
      <svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"></polyline></svg>
    </div>
    <h1>授权成功</h1>
    <p>可以关闭这个页面，回到终端继续使用 <code>tinia</code> 命令。</p>
    <div class="brand"><strong>TINIA</strong> &nbsp;·&nbsp; CLI</div>
  </main>
</body>
</html>`

const failureTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<title>Tinia · 授权失败</title>` + callbackBaseStyle + `
<style>
  .icon { background: linear-gradient(135deg, #f43f5e, #b91c1c); box-shadow: 0 0 44px rgba(244, 63, 94, 0.45); }
</style>
</head>
<body>
  <main class="card" role="alert">
    <div class="icon" aria-hidden="true">
      <svg viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
    </div>
    <h1>授权失败</h1>
    <p>这次登录没有完成，可关闭页面后重试 <code>tinia login</code>。</p>
    %s
    <div class="brand"><strong>TINIA</strong> &nbsp;·&nbsp; CLI</div>
  </main>
</body>
</html>`

// SuccessPage 返回授权成功页面 HTML。
func SuccessPage() string {
	return successHTML
}

// FailurePage 返回授权失败页面 HTML，reason 会显示在卡片上（已 HTML 转义）。
// 传空字符串则不显示原因块。
func FailurePage(reason string) string {
	block := ""
	if reason != "" {
		block = fmt.Sprintf(`<div class="reason">%s</div>`, html.EscapeString(reason))
	}
	return fmt.Sprintf(failureTemplate, block)
}
