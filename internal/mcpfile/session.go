// Package mcpfile 是 `tinia mcp file` —— 本地 stdio MCP server，给 AI 客户端提供
// 「碰本机文件」的两件事：把本地文件上传进 Tinia 数据源、把 Tinia 的 blob 拉到本机磁盘。
//
// 远程 MCP（{host}/api/v1/mcp）跑在服务器上，看不到用户机器上的文件，也不适合用
// JSON-RPC 内联 base64 传几百 MB 的数据，所以这两件事要一个跑在 AI 旁边的本地进程。
// 它原来是 Node 写的 tinia-file-mcp（Tinia_Local_MCP 仓），并进 CLI 之后客户机不再需要 Node。
//
// 工具名、参数、返回字段与 Node 版完全一致，skill 文档不用改。
// 只走 Tinia 的 MCP 数据面 /api/v1/mcp_data/*，鉴权与远程 MCP 同一套 OAuth。
package mcpfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bestfunc/tinia-cli/internal/auth"
)

// 本地文件 MCP 涉及的 scope（与 Node 版一致）：
//   - mcp:data       数据源浏览 —— fetch_blob 要；也是「token 能用」的最低门槛
//   - mcp:data_write 数据源写入 —— 只有 upload_file_to_datasource 要
//
// 授权时两个都申请（server 按用户组权限取交集），但只把 mcp:data 当门槛：
// 只开了只读权限的用户照样能 fetch_blob，不会因为拿不到写权限被反复拉去授权。
const (
	ScopeData      = "mcp:data"
	ScopeDataWrite = "mcp:data_write"
)

var (
	needScopes = []string{ScopeData}
	wantScopes = []string{ScopeData, ScopeDataWrite}
)

// errUnauthorized 数据面返回 401。上层据此清 token、重新授权、重试一次。
var errUnauthorized = errors.New("HTTP 401")

// Session 一次 `tinia mcp file` 进程的连接信息：连哪个 Tinia、用什么凭据。
type Session struct {
	Host       string // Tinia 地址（认证、元数据都走它），无尾斜杠
	UploadHost string // 数据面直连地址；空 = Host（只有本地开发要分开，见 DataBase）
	EnvToken   string // env TINIA_TOKEN；有就直接用，不走 OAuth
	HTTP       *http.Client

	mu      sync.Mutex
	desktop *bool // edition 探测结果缓存（nil = 还没探测）
}

// DataBase 数据面地址。本地开发时认证页是 Vite(:18722) 上的前端路由，但 Vite 的
// http-proxy 转发不了大 multipart（EPIPE），所以上传/下载直连 Go(:18721)。
// token 由同一个 Go server 签发校验，跨端口通用。
func (s *Session) DataBase() string {
	if s.UploadHost != "" {
		return s.UploadHost
	}
	return s.Host
}

// credential 本次请求用的凭据。Token 为空 = 桌面版 loopback trust，不带 Authorization。
type credential struct {
	Token  string
	Scopes []string // 已知授予的 scope；nil = 不知道（TINIA_TOKEN / 桌面版），交给 server 判
}

// credential 取凭据。reauth=true 表示刚收到 401：作废本地 token 后重新拿（先 refresh，不行再浏览器授权）。
func (s *Session) credential(ctx context.Context, reauth bool) (credential, error) {
	if s.EnvToken != "" {
		if reauth {
			return credential{}, errors.New("TINIA_TOKEN 无效或已过期（HTTP 401）—— 请到 Tinia 重新生成访问令牌")
		}
		return credential{Token: s.EnvToken}, nil
	}
	if s.isDesktop(ctx) {
		// 桌面版 daemon 对本机不带 token 的请求按本机超管处理（主仓 mcp/auth.go 的
		// desktopLoopbackInject）；本进程和 daemon 同机，等价于「我是本机主人」。
		return credential{}, nil
	}
	if reauth {
		if err := invalidateAccess(s.Host); err != nil {
			return credential{}, err
		}
	}
	ha, err := auth.EnsureScopes(ctx, s.Host, needScopes, wantScopes)
	if err != nil {
		return credential{}, err
	}
	return credential{Token: ha.AccessToken, Scopes: ha.ScopeList()}, nil
}

// invalidateAccess 把 access_token 标成已过期（保留 refresh_token 和 scope），
// 让下一次 EnsureScopes 先试 refresh。
func invalidateAccess(host string) error {
	ha, err := auth.Get(host)
	if err != nil || ha == nil {
		return err
	}
	ha.ExpiresAt = time.Time{}
	return auth.Put(host, ha)
}

// isDesktop host 是不是本机的桌面版 daemon。
//
// loopback ≠ 桌面版：本地开发（Vite :18722）、server 版同样可能跑在本机，它们不接受
// 不带 token 的请求。所以 loopback 之外还要实测 /api/v1/meta 的 edition。
// 结果按进程缓存；探测失败不缓存（daemon 可能还没起来），按非桌面处理。
func (s *Session) isDesktop(ctx context.Context) bool {
	if !isLoopbackHost(s.Host) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.desktop != nil {
		return *s.desktop
	}
	m, err := s.fetchMeta(ctx, 3*time.Second)
	if err != nil {
		logf("探测 %s 的 edition 失败（%v），按非桌面版走 OAuth", s.Host, err)
		return false
	}
	d := m.Edition == "desktop"
	s.desktop = &d
	if d {
		logf("%s 是桌面单机版，跳过 OAuth（走 daemon loopback trust）", s.Host)
	} else {
		logf("%s 是本机但 edition=%s，走标准 OAuth", s.Host, m.Edition)
	}
	return d
}

func isLoopbackHost(host string) bool {
	u, err := url.Parse(host)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// appMeta GET /api/v1/meta 里本工具关心的字段（公开接口，不需要登录）。
type appMeta struct {
	Edition string `json:"edition"`
	Version string `json:"version"`
}

func (s *Session) fetchMeta(ctx context.Context, timeout time.Duration) (*appMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", s.Host+"/api/v1/meta", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var env struct {
		Data appMeta `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("meta 不是 JSON: %w", err)
	}
	return &env.Data, nil
}

// withAuth 带着凭据跑 fn；fn 返回 errUnauthorized 时清 token、重新授权、重试一次（同 Node 版）。
func (s *Session) withAuth(ctx context.Context, fn func(credential) error) error {
	cred, err := s.credential(ctx, false)
	if err != nil {
		return err
	}
	err = fn(cred)
	if !errors.Is(err, errUnauthorized) {
		return err
	}
	logf("收到 401，清除本地 token 重新授权后重试")
	cred, err2 := s.credential(ctx, true)
	if err2 != nil {
		return fmt.Errorf("%v；重新授权失败: %w", err, err2)
	}
	return fn(cred)
}

func setAuthHeader(req *http.Request, c credential) {
	// 桌面版 token 为空时不能带 "Bearer "：daemon 会拿空串去校验，直接 401
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// logf 写 stderr。stdout 是 MCP 协议通道，任何别的东西写上去都会让客户端解析失败。
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[tinia mcp file] "+format+"\n", args...)
}

func hasScope(scopes []string, s string) bool {
	for _, x := range scopes {
		if x == s {
			return true
		}
	}
	return false
}

func trimHost(h string) string {
	return strings.TrimRight(strings.TrimSpace(h), "/")
}
