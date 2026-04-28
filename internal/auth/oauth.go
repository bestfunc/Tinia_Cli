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
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	clientName    = "Tinia CLI"
	defaultScopes = "mcp:dev mcp:nodes mcp:flow"
)

// Login 跑完整 OAuth 流程，存好 token。
func Login(ctx context.Context, host, scopes string) (*HostAuth, error) {
	host = strings.TrimRight(host, "/")
	if scopes == "" {
		scopes = defaultScopes
	}

	// 1. 起本地 callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("无法起本地端口: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// 2. DCR 注册临时 client
	clientID, err := registerClient(ctx, host, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("DCR 注册失败: %w", err)
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

	// 4. 拼授权 URL + 打开浏览器
	authURL := host + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	fmt.Printf("→ 在浏览器中打开授权页：\n  %s\n", authURL)
	_ = openBrowser(authURL)

	// 5. 等 callback
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
				errCh <- fmt.Errorf("授权被拒绝: %s — %s", e, q.Get("error_description"))
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<h2>授权失败，可关闭此窗口</h2>"))
				return
			}
			if q.Get("state") != state {
				errCh <- fmt.Errorf("state 不匹配（CSRF 防护拦截）")
				return
			}
			code := q.Get("code")
			if code == "" {
				errCh <- fmt.Errorf("回调缺少 code 参数")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<h2>✓ 授权成功，可关闭此窗口回到终端</h2>`))
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
	if err := Put(host, ha); err != nil {
		return nil, err
	}
	return ha, nil
}

// EnsureValid 取 host 的 token；过期则自动 refresh。
func EnsureValid(ctx context.Context, host string) (*HostAuth, error) {
	host = strings.TrimRight(host, "/")
	ha, err := Get(host)
	if err != nil {
		return nil, err
	}
	if ha == nil {
		return nil, fmt.Errorf("未登录 %s，请先 tinia login --host %s", host, host)
	}
	if ha.Expired() {
		return Refresh(ctx, host)
	}
	return ha, nil
}

// ===== private helpers =====

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
