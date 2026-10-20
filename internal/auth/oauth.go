// OAuth 2.1 授权码 + PKCE + 动态客户端注册（RFC 7591 DCR）流程。
//
// 流程：
//  1. POST {host}/api/v1/oauth/register 注册一个临时 client（RFC 7591）
//  2. 起本地 HTTP server 监听 127.0.0.1:<port>，做 redirect_uri callback
//  3. 浏览器打开 {host}/oauth/authorize?response_type=code&...&code_challenge=...
//  4. 用户登录 + 同意 → 浏览器跳回 127.0.0.1:<port>?code=...&state=...
//  5. POST {host}/api/v1/oauth/token (grant_type=authorization_code) 换 token
//  6. 存 ~/.tinia/auth.json
//
// scopes 默认申请 mcp:dev + mcp:nodes + mcp:flow（CLI 主要用 dev_*，附加 nodes / flow
// 让 tinia run / tinia logs 也能用）。

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	clientName    = "Tinia CLI"
	defaultScopes = "mcp:dev mcp:nodes mcp:flow"

	// desktopClientID 是主仓 migration 0055 注册的内置 OAuth client。
	// CLI 关联本机 desktop 时直接 hardcode，不走 DCR —— desktop 是单用户环境，
	// loopback 信任边界内安全（PKCE 防 code 拦截，state 防 CSRF）。
	desktopClientID = "tinia-cli-desktop"
)

// Out 是登录流程给人看的提示（「在浏览器中打开授权页」等）写到哪。
// 默认 stdout；`tinia mcp file` 这类 stdio MCP server 的 stdout 是协议通道，
// 必须改成 os.Stderr，否则一行提示就会把客户端的 JSON-RPC 解析弄坏。
var Out io.Writer = os.Stdout

// BrowserOpener 打开授权页的方式；测试里换成直接 GET 授权 URL 的假浏览器。
var BrowserOpener = openBrowser

// Login 跑完整 OAuth 流程，存好 token。
//
// 自动识别 host 的 edition：
//   - desktop  → 用 `tinia://` URL scheme 唤起 Wails app 内授权（已登录 session 直接生效）
//   - 其他      → 系统浏览器走标准 OAuth + DCR
//
// 若 meta 检测失败，降级为标准流程。
func Login(ctx context.Context, host, scopes string) (*HostAuth, error) {
	host = strings.TrimRight(host, "/")
	if scopes == "" {
		scopes = defaultScopes
	}

	// 探测 host edition：desktop 走 app 内授权（避开系统浏览器 vs webview 双 session 问题）
	isDesktop := detectDesktopEdition(ctx, host)

	// 1. 起本地 callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("无法起本地端口: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// 2. 拿 client_id：desktop 用内置 hardcode，其他走 DCR
	var clientID string
	if isDesktop {
		clientID = desktopClientID
	} else {
		clientID, err = registerClient(ctx, host, redirectURI)
		if err != nil {
			return nil, fmt.Errorf("DCR 注册失败: %w", err)
		}
	}

	// 3. PKCE
	verifier, challenge, err := NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := RandomString(16)
	if err != nil {
		return nil, err
	}

	// 4. 拼授权 query
	authQuery := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authPath := "/oauth/authorize?" + authQuery
	authURL := host + authPath

	// 5. 唤起授权页：desktop 走 Wails IPC（让 app 内 webview 跳页），其他走系统浏览器
	//
	// desktop 不能用 tinia:// URL scheme —— macOS LaunchServices 在 ad-hoc 签名下
	// 不尊重 LSMultipleInstancesProhibited，每次 open tinia:// 都启第二个 Tinia.app
	// 实例 → 老主进程被 macOS 资源冲突 kill → 用户看到闪退。
	//
	// 改走 daemon 锁文件里的 Wails IPC HTTP 端口 —— 直接告诉已跑的 app 跳路由，
	// 不触发 LaunchServices，不启第二个实例。
	if isDesktop {
		if tryDesktopNavigate(ctx, authPath) {
			fmt.Fprintf(Out, "→ 已在 Tinia 桌面 app 内打开授权页面，请确认\n")
		} else {
			fmt.Fprintf(Out, "→ 未能与 Tinia 桌面 app 通信，请手动复制下面 URL 到 app 内浏览器：\n  %s\n", authURL)
			_ = BrowserOpener(authURL)
		}
	} else {
		fmt.Fprintf(Out, "→ 在浏览器中打开授权页（打不开就手动复制到浏览器）：\n  %s\n", authURL)
		_ = BrowserOpener(authURL)
	}

	// 5. 等 callback
	// desktop 场景下 webview 加载 callback HTML（不会自动关），SuccessPage / FailurePage
	// 需要"返回 Tinia 首页"按钮跳回 host/；非 desktop（系统浏览器）传空 host，
	// 显示原版"关闭页面" 文案。
	//
	// host 里 localhost 要规范化到 127.0.0.1 —— daemon 实际监听 127.0.0.1，
	// webview 原本就在那个 origin 下登录。如果按钮跳 localhost，浏览器视为
	// 不同 origin（host header 不同），cookie 不共享 → React 检测未登录 →
	// 跳到 /login，用户感觉"又得登录"。
	backHost := ""
	if isDesktop {
		backHost = strings.Replace(host, "//localhost", "//127.0.0.1", 1)
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				desc := q.Get("error_description")
				errCh <- fmt.Errorf("授权被拒绝: %s — %s", e, desc)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				reason := e
				if desc != "" {
					reason = e + ": " + desc
				}
				_, _ = w.Write([]byte(FailurePage(reason, backHost)))
				return
			}
			if q.Get("state") != state {
				errCh <- fmt.Errorf("state 不匹配（CSRF 防护拦截）")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(FailurePage("state mismatch — 可能是 CSRF 防护拦截，请重试", backHost)))
				return
			}
			code := q.Get("code")
			if code == "" {
				errCh <- fmt.Errorf("回调缺少 code 参数")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(FailurePage("回调缺少 code 参数", backHost)))
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(SuccessPage(backHost)))
			codeCh <- code
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("授权超时（5 分钟），请重试")
	}

	// 6. 换 token
	tok, err := exchangeToken(ctx, host, clientID, code, verifier, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("换 token 失败: %w", err)
	}

	ha := &HostAuth{
		ClientID:     clientID,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       grantedScopes(tok.Scope, scopes),
	}
	if err := Put(host, ha); err != nil {
		return nil, fmt.Errorf("存 token 失败: %w", err)
	}
	return ha, nil
}

// Refresh 用 refresh_token 换新 access_token。
func Refresh(ctx context.Context, host string) (*HostAuth, error) {
	ha, err := Get(host)
	if err != nil {
		return nil, err
	}
	if ha == nil || ha.RefreshToken == "" {
		return nil, fmt.Errorf("没找到 refresh_token，请重新 tinia login")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {ha.RefreshToken},
		"client_id":     {ha.ClientID},
	}
	tok, err := postForm(ctx, host+"/api/v1/oauth/token", form)
	if err != nil {
		return nil, err
	}
	ha.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		ha.RefreshToken = tok.RefreshToken
	}
	ha.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.Scope != "" {
		ha.Scopes = splitScopes(tok.Scope)
	}
	if err := Put(host, ha); err != nil {
		return nil, err
	}
	return ha, nil
}

// EnsureValid 取 host 的 token；
//   - 过期了先尝试用 refresh_token 续期
//   - refresh_token 也过期 / invalid_grant → 自动跳浏览器走完整 OAuth Login，
//     不要把错误抛给用户让他自己 tinia login
//   - 完全没登录过 → 同样直接走 Login（首次跑 init / clone / push 时就别报错）
func EnsureValid(ctx context.Context, host string) (*HostAuth, error) {
	host = strings.TrimRight(host, "/")
	ha, err := Get(host)
	if err != nil {
		return nil, err
	}
	if ha == nil {
		fmt.Fprintf(Out, "→ 未登录 %s，跳转浏览器登录...\n", host)
		return Login(ctx, host, "")
	}
	if ha.Expired() {
		refreshed, refreshErr := Refresh(ctx, host)
		if refreshErr == nil {
			return refreshed, nil
		}
		// refresh 失败的常见原因：refresh_token 过期 / 被吊销 / 服务端 client 被清。
		// 不要 fail —— 自动重新走授权码 + PKCE 流程。
		fmt.Fprintf(Out, "→ 登录会话已过期（%v），重新跳转浏览器登录...\n", refreshErr)
		return Login(ctx, host, "")
	}
	return ha, nil
}

// EnsureScopes 像 EnsureValid 一样取 host 的有效 token，但额外保证 token 至少带 need 里
// 的每个 scope；缺了就重新授权，申请的是「已有 scope ∪ want」的**并集**。
//
// 为什么必须并集：auth.json 一个 host 只存一组 token。`tinia mcp file` 要 mcp:data，
// 如果只申请 mcp:data，新 token 会把 `tinia login` 拿到的 mcp:dev 等冲掉，下次
// tinia push 就 403。没登录过时用 defaultScopes ∪ want，理由相同。
//
// need 是「token 能用」的最低门槛，want 是顺带申请的（server 会按用户组权限取交集，
// 申请了不一定给）。两者分开，是为了只开了只读权限的用户不会因为拿不到 want 里的
// 写权限而每次都被拉去重新授权。
func EnsureScopes(ctx context.Context, host string, need, want []string) (*HostAuth, error) {
	host = strings.TrimRight(host, "/")
	ha, err := Get(host)
	if err != nil {
		return nil, err
	}
	if ha != nil && ha.Expired() {
		if refreshed, refreshErr := Refresh(ctx, host); refreshErr == nil {
			ha = refreshed
		} else {
			fmt.Fprintf(Out, "→ 登录会话已过期（%v），重新跳转浏览器登录...\n", refreshErr)
			ha = &HostAuth{Scopes: ha.ScopeList()} // 只留旧 scope 用于并集，token 作废
		}
	}
	if ha != nil && ha.AccessToken != "" && ha.HasScopes(need) {
		return ha, nil
	}

	base := splitScopes(defaultScopes)
	if ha != nil {
		base = ha.ScopeList()
	}
	scopes := strings.Join(unionScopes(base, want, need), " ")
	if ha == nil {
		fmt.Fprintf(Out, "→ 未登录 %s，跳转浏览器登录...\n", host)
	} else if ha.AccessToken != "" {
		fmt.Fprintf(Out, "→ 当前授权缺少 %s，重新授权（申请 %s）...\n", strings.Join(missingScopes(ha.ScopeList(), need), " "), scopes)
	}
	return Login(ctx, host, scopes)
}

// ===== private helpers =====

// grantedScopes 取 token 响应里 server 实际授予的 scope；server 没回 scope 字段时
// 退回到申请的那组（RFC 6749 §5.1：省略 scope 表示与申请的相同）。
func grantedScopes(granted, requested string) []string {
	if granted != "" {
		return splitScopes(granted)
	}
	return splitScopes(requested)
}

func splitScopes(s string) []string {
	return strings.Fields(s)
}

// unionScopes 按出现顺序合并去重。
func unionScopes(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, s := range l {
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func missingScopes(have, need []string) []string {
	got := map[string]bool{}
	for _, s := range have {
		got[s] = true
	}
	var out []string
	for _, s := range need {
		if !got[s] {
			out = append(out, s)
		}
	}
	return out
}

// tryDesktopNavigate 通过 Tinia 桌面 app 的 IPC 端口让 webview 跳到指定路径。
// 端口写在锁文件 $UserCacheDir/Tinia/desktop.lock 里（main 仓 desktop/single_instance.go
// 创建），调 POST /navigate?path=...
//
// 成功 → 返回 true（webview 内部已 location.href = path）
// 失败（锁文件不在 / 端口连不通 / 非 200） → 返回 false，调用方应降级
func tryDesktopNavigate(ctx context.Context, path string) bool {
	lockPath := desktopLockFilePath()
	if lockPath == "" {
		return false
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	var lock struct {
		Addr string `json:"addr"`
	}
	if err := json.Unmarshal(data, &lock); err != nil || lock.Addr == "" {
		return false
	}

	ipcURL := lock.Addr + "/navigate?path=" + url.QueryEscape(path)
	req, err := http.NewRequestWithContext(ctx, "POST", ipcURL, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// desktopLockFilePath 返回 Tinia 桌面 app 的锁文件路径，跟主仓
// desktop/single_instance.go 的 lockFilePath() 必须保持一致。
func desktopLockFilePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return dir + "/Tinia/desktop.lock"
}

// detectDesktopEdition GET /api/v1/meta，返回 host 是否是 desktop 实例。
// 任何网络 / 解析错误一律按 false 处理，让流程降级到标准 OAuth。
//
// 主仓返回结构是 {"code":0,"data":{"edition":"desktop",...}}，所以要解 data 字段
// 里的 edition，不是 root。
func detectDesktopEdition(ctx context.Context, host string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", host+"/api/v1/meta", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Data struct {
			Edition string `json:"edition"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return envelope.Data.Edition == "desktop"
}

func registerClient(ctx context.Context, host, redirectURI string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "none", // PKCE 公开客户端
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/v1/oauth/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("响应缺 client_id: %s", string(respBody))
	}
	return out.ClientID, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func exchangeToken(ctx context.Context, host, clientID, code, verifier, redirectURI string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	return postForm(ctx, host+"/api/v1/oauth/token", form)
}

func postForm(ctx context.Context, url string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("响应缺 access_token: %s", string(body))
	}
	return &tok, nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default: // linux + 其他 unix
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
