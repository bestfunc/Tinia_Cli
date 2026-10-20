package mcpfile

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bestfunc/tinia-cli/internal/auth"
)

// fakeTinia 假 Tinia server：meta、OAuth（register/token）、数据面 blob-proxy 与上传。
type fakeTinia struct {
	t       *testing.T
	srv     *httptest.Server
	edition string

	mu            sync.Mutex
	validTokens   map[string]bool // 数据面接受的 access token
	tokenSeq      int
	lastScope     string   // 最近一次授权码换 token 时申请的 scope
	authHeaders   []string // 数据面收到的 Authorization 头
	oauthCalls    int      // register + token 调用次数
	uploads       []fakeUpload
	blob          []byte
	blobShort     bool // 声明的 Content-Length 比实际写的多（模拟中途断连）
	refreshGrants int
}

type fakeUpload struct {
	filename      string
	sha           string
	size          int64
	contentLength int64
	chunked       bool
}

func newFakeTinia(t *testing.T, edition string) *fakeTinia {
	f := &fakeTinia{t: t, edition: edition, validTokens: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/meta", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"code":0,"data":{"edition":%q,"version":"v1.49.7"}}`, f.edition)
	})
	mux.HandleFunc("/api/v1/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.oauthCalls++
		f.mu.Unlock()
		fmt.Fprint(w, `{"client_id":"tmc-test"}`)
	})
	mux.HandleFunc("/api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.oauthCalls++
		f.tokenSeq++
		tok := fmt.Sprintf("tma-%d", f.tokenSeq)
		f.validTokens[tok] = true
		scope := f.lastScope
		if r.Form.Get("grant_type") == "refresh_token" {
			f.refreshGrants++
			scope = "" // 刷新时 server 不回 scope：客户端应保留原 scope
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": tok, "refresh_token": "tmr-" + tok, "token_type": "Bearer",
			"expires_in": 3600, "scope": scope,
		})
	})
	mux.HandleFunc("/api/v1/mcp_data/blob-proxy", func(w http.ResponseWriter, r *http.Request) {
		if !f.authorize(w, r) {
			return
		}
		f.mu.Lock()
		blob, short := f.blob, f.blobShort
		f.mu.Unlock()
		if short {
			w.Header().Set("Content-Length", fmt.Sprint(len(blob)+1000))
			w.WriteHeader(200)
			w.Write(blob)
			// 少写 1000 字节就返回：net/http 会关闭连接，客户端读到 unexpected EOF
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(blob)))
		w.Write(blob)
	})
	mux.HandleFunc("/api/v1/mcp_data/datasources/", func(w http.ResponseWriter, r *http.Request) {
		if !f.authorize(w, r) {
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		part, err := mr.NextPart()
		if err != nil || part.FormName() != "files" {
			http.Error(w, "missing files part", 400)
			return
		}
		h := sha256.New()
		n, _ := io.Copy(h, part)
		up := fakeUpload{
			filename: part.FileName(), sha: hex.EncodeToString(h.Sum(nil)), size: n,
			contentLength: r.ContentLength, chunked: len(r.TransferEncoding) > 0,
		}
		f.mu.Lock()
		f.uploads = append(f.uploads, up)
		f.mu.Unlock()
		fmt.Fprintf(w, `{"code":0,"message":"ok","data":{"uploaded":1,"files":[{"id":9,"filename":%q,"size_bytes":%d}]}}`, up.filename, n)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// authorize 数据面鉴权：桌面版不带 token 放行；否则 token 必须在 validTokens 里，不在就 401。
func (f *fakeTinia) authorize(w http.ResponseWriter, r *http.Request) bool {
	h := r.Header.Get("Authorization")
	f.mu.Lock()
	f.authHeaders = append(f.authHeaders, h)
	ok := (h == "" && f.edition == "desktop") || f.validTokens[strings.TrimPrefix(h, "Bearer ")]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"invalid_token"}`)
	}
	return ok
}

// fakeBrowser 代替真浏览器：记下申请的 scope，直接带 code 回调 CLI 的 loopback 地址。
func (f *fakeTinia) fakeBrowser(authURL string) error {
	u, err := url.Parse(authURL)
	if err != nil {
		return err
	}
	q := u.Query()
	f.mu.Lock()
	f.lastScope = q.Get("scope")
	f.mu.Unlock()
	go func() {
		resp, err := http.Get(q.Get("redirect_uri") + "?code=c1&state=" + url.QueryEscape(q.Get("state")))
		if err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

// setup 隔离 HOME（auth.json 写到临时目录），换掉浏览器，静音登录提示。
func setup(t *testing.T, f *fakeTinia) *Session {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	auth.Out = io.Discard
	auth.BrowserOpener = f.fakeBrowser
	return &Session{Host: f.srv.URL, HTTP: &http.Client{}}
}

func seedAuth(t *testing.T, host string, ha *auth.HostAuth) {
	t.Helper()
	if err := auth.Put(host, ha); err != nil {
		t.Fatal(err)
	}
}

func callJSON(t *testing.T, s *Server, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	res := s.callTool(context.Background(), name, args)
	text := res["content"].([]map[string]any)[0]["text"].(string)
	isErr, _ := res["isError"].(bool)
	var m map[string]any
	if !isErr {
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			t.Fatalf("%s 返回不是 JSON: %s", name, text)
		}
	}
	return m, text, isErr
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func validAuth(token string, scopes ...string) *auth.HostAuth {
	return &auth.HostAuth{ClientID: "tmc-old", AccessToken: token, RefreshToken: "tmr-old", ExpiresAt: time.Now().Add(time.Hour), Scopes: scopes}
}

// ===== 两个工具的成功路径 =====

func TestUpload_成功且流式带ContentLength(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	f.validTokens["tma-ok"] = true
	seedAuth(t, f.srv.URL, validAuth("tma-ok", "mcp:dev", ScopeData, ScopeDataWrite))

	data := []byte(strings.Repeat("tinia-wav-bytes;", 50000))
	p := writeTemp(t, "rec 01\".wav", data)
	m, text, isErr := callJSON(t, NewServer(sess, "vtest"), "upload_file_to_datasource",
		map[string]any{"datasource_id": float64(42), "local_path": p})
	if isErr {
		t.Fatalf("上传失败: %s", text)
	}
	if m["uploaded"].(float64) != 1 {
		t.Errorf("返回应原样透传 server 的 data，实际 %s", text)
	}
	up := f.uploads[0]
	sum := sha256.Sum256(data)
	if up.sha != hex.EncodeToString(sum[:]) || up.size != int64(len(data)) {
		t.Errorf("server 收到的内容不对：size=%d sha=%s", up.size, up.sha)
	}
	if up.filename != `rec 01".wav` {
		t.Errorf("文件名 = %q（引号要正确转义）", up.filename)
	}
	if up.chunked || up.contentLength <= int64(len(data)) {
		t.Errorf("应带准确 Content-Length 而非 chunked：chunked=%v len=%d", up.chunked, up.contentLength)
	}
}

func TestFetchBlob_成功(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	f.validTokens["tma-ok"] = true
	seedAuth(t, f.srv.URL, validAuth("tma-ok", ScopeData))
	f.blob = append([]byte("PK\x03\x04"), []byte(strings.Repeat("x", 100))...)

	m, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
		map[string]any{"blob_uri": "minio://tinia-blobs/blobs/70/abc", "include_base64": true})
	if isErr {
		t.Fatalf("fetch_blob 失败: %s", text)
	}
	sum := sha256.Sum256(f.blob)
	want := hex.EncodeToString(sum[:])
	if m["sha256"] != want || m["format"] != "npz" || m["size_bytes"].(float64) != float64(len(f.blob)) {
		t.Errorf("结果不对: %s", text)
	}
	saved := m["saved_path"].(string)
	if filepath.Base(saved) != "tinia-blob-"+want[:16]+".npz" {
		t.Errorf("默认落地名 = %s", saved)
	}
	got, _ := os.ReadFile(saved)
	if string(got) != string(f.blob) {
		t.Error("落地内容与 blob 不一致")
	}
	os.Remove(saved)
	if m["base64"] == nil {
		t.Error("include_base64 且 <64KB 时应带 base64")
	}
	// 字段顺序与 Node 版一致
	keys := []string{"blob_uri", "size_bytes", "format", "format_hint", "sha256", "saved_path", "base64"}
	last := -1
	for _, k := range keys {
		i := strings.Index(text, `"`+k+`"`)
		if i < last {
			t.Errorf("字段 %s 顺序不对:\n%s", k, text)
		}
		last = i
	}
}

func TestFetchBlob_长度不符报错且不留半截(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	f.validTokens["tma-ok"] = true
	seedAuth(t, f.srv.URL, validAuth("tma-ok", ScopeData))
	f.blob = []byte(strings.Repeat("y", 5000))
	f.blobShort = true

	dst := filepath.Join(t.TempDir(), "out.npz")
	_, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
		map[string]any{"blob_uri": "minio://b/1", "save_path": dst})
	if !isErr || !strings.Contains(text, "不完整") {
		t.Fatalf("应报「下载不完整」，实际 isErr=%v %s", isErr, text)
	}
	for _, p := range []string{dst, dst + ".part"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("不应留下 %s", p)
		}
	}
}

// ===== 鉴权 =====

func TestAuth_401后清token刷新并重试一次(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	// tma-stale 在本地看着没过期，但 server 已不认（被吊销）
	seedAuth(t, f.srv.URL, validAuth("tma-stale", ScopeData))
	f.blob = []byte("{}")

	m, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
		map[string]any{"blob_uri": "minio://b/2", "save_path": filepath.Join(t.TempDir(), "o.json")})
	if isErr {
		t.Fatalf("401 后应自动恢复: %s", text)
	}
	if m["format"] != "json" {
		t.Errorf("format = %v", m["format"])
	}
	if len(f.authHeaders) != 2 || f.authHeaders[0] != "Bearer tma-stale" || f.authHeaders[1] != "Bearer tma-1" {
		t.Errorf("应恰好重试一次，且第二次用刷新得到的新 token：%v", f.authHeaders)
	}
	if f.refreshGrants != 1 {
		t.Errorf("应先走 refresh_token，实际 refresh 次数 %d", f.refreshGrants)
	}
	ha, _ := auth.Get(f.srv.URL)
	if !ha.HasScopes([]string{ScopeData}) {
		t.Errorf("refresh 后 scope 不应丢：%v", ha.Scopes)
	}
}

func TestAuth_桌面版不带token也不登录(t *testing.T) {
	f := newFakeTinia(t, "desktop")
	sess := setup(t, f)
	f.blob = []byte("RIFF....WAVE")

	_, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
		map[string]any{"blob_uri": "minio://b/3", "save_path": filepath.Join(t.TempDir(), "a.wav")})
	if isErr {
		t.Fatalf("桌面版应直接成功: %s", text)
	}
	if f.authHeaders[0] != "" {
		t.Errorf("桌面版不应带 Authorization，实际 %q", f.authHeaders[0])
	}
	if f.oauthCalls != 0 {
		t.Errorf("桌面版不应走 OAuth，实际调用 %d 次", f.oauthCalls)
	}
	p := writeTemp(t, "a.bin", []byte("abc"))
	if _, text, isErr := callJSON(t, NewServer(sess, "vtest"), "upload_file_to_datasource",
		map[string]any{"datasource_id": float64(1), "local_path": p}); isErr {
		t.Fatalf("桌面版上传应成功（scope 未知时交给 server 判）: %s", text)
	}
}

func TestAuth_TINIA_TOKEN优先且401不重试(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	f.validTokens["tma-pat"] = true
	f.validTokens["tma-oauth"] = true
	seedAuth(t, f.srv.URL, validAuth("tma-oauth", ScopeData, ScopeDataWrite))
	sess.EnvToken = "tma-pat"
	f.blob = []byte("PAR1")

	if _, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
		map[string]any{"blob_uri": "minio://b/4", "save_path": filepath.Join(t.TempDir(), "p")}); isErr {
		t.Fatalf("fetch_blob 失败: %s", text)
	}
	if f.authHeaders[0] != "Bearer tma-pat" {
		t.Errorf("有 TINIA_TOKEN 时应优先用它，实际 %q", f.authHeaders[0])
	}

	sess.EnvToken = "tma-revoked"
	_, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
		map[string]any{"blob_uri": "minio://b/4", "save_path": filepath.Join(t.TempDir(), "q")})
	if !isErr || !strings.Contains(text, "TINIA_TOKEN") {
		t.Errorf("TINIA_TOKEN 失效应明确报错而不是跳浏览器: %s", text)
	}
	if f.oauthCalls != 0 {
		t.Errorf("用 TINIA_TOKEN 时不应走 OAuth，实际 %d 次", f.oauthCalls)
	}
}

func TestAuth_缺数据scope时申请并集不丢旧scope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string // nil = 老版本 CLI 写的 auth.json（没有 scopes 字段）
	}{
		{"已记录 scope", []string{"mcp:dev", "mcp:nodes", "mcp:flow", "mcp:graphs"}},
		{"老 auth.json 无 scopes 字段", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTinia(t, "server")
			sess := setup(t, f)
			f.validTokens["tma-dev"] = true
			seedAuth(t, f.srv.URL, validAuth("tma-dev", tc.scopes...))
			f.blob = []byte("{}")

			if _, text, isErr := callJSON(t, NewServer(sess, "vtest"), "fetch_blob",
				map[string]any{"blob_uri": "minio://b/5", "save_path": filepath.Join(t.TempDir(), "r")}); isErr {
				t.Fatalf("补授权后应成功: %s", text)
			}
			requested := strings.Fields(f.lastScope)
			want := []string{"mcp:dev", "mcp:nodes", "mcp:flow", ScopeData, ScopeDataWrite}
			if tc.scopes != nil {
				want = append(want, "mcp:graphs")
			}
			for _, s := range want {
				if !hasScope(requested, s) {
					t.Errorf("补授权申请的 scope 缺 %s：%v", s, requested)
				}
			}
			ha, _ := auth.Get(f.srv.URL)
			for _, s := range want {
				if !hasScope(ha.Scopes, s) {
					t.Errorf("auth.json 里 scope 缺 %s（tinia login 的权限被冲掉了）：%v", s, ha.Scopes)
				}
			}
		})
	}
}

func TestUpload_缺写权限给清晰提示(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	f.validTokens["tma-ro"] = true
	seedAuth(t, f.srv.URL, validAuth("tma-ro", ScopeData))
	p := writeTemp(t, "a.wav", []byte("RIFF"))

	_, text, isErr := callJSON(t, NewServer(sess, "vtest"), "upload_file_to_datasource",
		map[string]any{"datasource_id": float64(1), "local_path": p})
	if !isErr || !strings.Contains(text, "mcp:data_write") {
		t.Errorf("应提示缺 mcp:data_write: %s", text)
	}
	if len(f.uploads) != 0 || f.oauthCalls != 0 {
		t.Errorf("只读用户不应发起上传，也不应被拉去重新授权")
	}
}

func TestAgentportStatus(t *testing.T) {
	f := newFakeTinia(t, "server")
	sess := setup(t, f)
	m, text, isErr := callJSON(t, NewServer(sess, "v0.2.0"), "agentport_status", nil)
	if isErr || m["cli_version"] != "v0.2.0" || m["app_version"] != "v1.49.7" || m["app_edition"] != "server" || m["app_reachable"] != true {
		t.Errorf("agentport_status = %s", text)
	}
}

// ===== stdio JSON-RPC =====

func TestServe_协议往返且stdout只有JSONRPC(t *testing.T) {
	f := newFakeTinia(t, "desktop")
	sess := setup(t, f)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"s3","method":"tools/call","params":{"name":"agentport_status","arguments":{}}}`,
		`[{"jsonrpc":"2.0","id":4,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`,
		`{"jsonrpc":"2.0","id":5,"method":"nope"}`,
		`not json`,
	}, "\n") + "\n"
	var out strings.Builder
	if err := NewServer(sess, "vtest").Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}

	byID := map[string]json.RawMessage{}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	lines := 0
	for sc.Scan() {
		lines++
		line := sc.Bytes()
		if line[0] == '[' {
			var arr []map[string]json.RawMessage
			if err := json.Unmarshal(line, &arr); err != nil || len(arr) != 1 {
				t.Fatalf("batch 响应不对: %s", line)
			}
			byID[string(arr[0]["id"])] = line
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(line, &m); err != nil || string(m["jsonrpc"]) != `"2.0"` {
			t.Fatalf("stdout 上出现了非 JSON-RPC 的行: %q", line)
		}
		byID[string(m["id"])] = line
	}
	if lines != 6 { // 两个通知不回
		t.Errorf("应回 6 行，实际 %d:\n%s", lines, out.String())
	}
	if !strings.Contains(string(byID["1"]), `"protocolVersion":"2025-06-18"`) || !strings.Contains(string(byID["1"]), `"version":"vtest"`) {
		t.Errorf("initialize: %s", byID["1"])
	}
	for _, name := range []string{"upload_file_to_datasource", "fetch_blob", "agentport_status"} {
		if !strings.Contains(string(byID["2"]), `"name":"`+name+`"`) {
			t.Errorf("tools/list 缺 %s", name)
		}
	}
	if !strings.Contains(string(byID[`"s3"`]), "app_version") {
		t.Errorf("tools/call: %s", byID[`"s3"`])
	}
	if !strings.Contains(string(byID["5"]), "-32601") || !strings.Contains(string(byID["null"]), "-32700") {
		t.Errorf("未知方法 / 解析错误: %s %s", byID["5"], byID["null"])
	}
}

func TestNegotiateProtocolVersion(t *testing.T) {
	for req, want := range map[string]string{
		"2025-11-25": "2025-11-25", "2025-03-26": "2025-03-26", "2024-11-05": "2024-11-05",
		"2026-07-28": "2025-11-25", "2025-01-01": "2024-11-05", "2020-01-01": "2025-11-25", "": "2025-11-25",
	} {
		if got := negotiateProtocolVersion(req); got != want {
			t.Errorf("negotiate(%q) = %q, want %q", req, got, want)
		}
	}
}
