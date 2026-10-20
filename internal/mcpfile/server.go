package mcpfile

// stdio JSON-RPC 2.0：一行一条消息（规范要求消息内不得有换行），stdout 只写协议消息。
// 规范：https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#stdio
//
// 请求并发处理：一次几百 MB 的上传可能跑好几分钟，期间客户端发来的 ping /
// notifications/cancelled 不能被它堵住。写 stdout 加锁，保证一条消息一行、不交错。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"sync"
)

// supportedProtocolVersions initialize 里愿意回的版本，新的在前。
// stdio 下 2025-03-26 的 batch（一行一个数组）我们也解析，所以四个版本都满足；
// 2026-07-28 去掉了 initialize 握手、要求 server/discover，没做。
var supportedProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

var protocolVersionRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// negotiateProtocolVersion 客户端要的版本支持就原样回；否则回不晚于它的最新支持版，
// 没有更早的才回最新（与主仓 server/internal/mcp/transport.go 同一规则）。
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return v
		}
	}
	if protocolVersionRe.MatchString(requested) {
		for _, v := range supportedProtocolVersions {
			if v < requested {
				return v
			}
		}
	}
	return supportedProtocolVersions[0]
}

const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Server 一个 stdio MCP 会话。
type Server struct {
	sess    *Session
	version string // CLI 版本（main.version），即 serverInfo.version

	outMu sync.Mutex
	out   *bufio.Writer

	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc // 请求 id（原始 JSON）→ 取消函数
}

func NewServer(sess *Session, version string) *Server {
	return &Server{sess: sess, version: version, inflight: map[string]context.CancelFunc{}}
}

// Serve 读 in 直到 EOF，响应写 out。stdin 关闭即退出（先等手上的请求做完或被取消）。
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = bufio.NewWriter(out)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadBytes('\n') // 不用 Scanner：它默认一行上限 64KB
		if line = bytes.TrimSpace(line); len(line) > 0 {
			s.handleLine(ctx, line, &wg)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			cancel()
			wg.Wait()
			return err
		}
	}
	wg.Wait()
	return nil
}

func (s *Server) handleLine(ctx context.Context, line []byte, wg *sync.WaitGroup) {
	if line[0] == '[' {
		// JSON-RPC batch（2025-03-26 允许）：逐条处理，响应收齐后作为一个数组回
		var msgs []rpcMessage
		if err := json.Unmarshal(line, &msgs); err != nil || len(msgs) == 0 {
			s.write(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{errParse, "parse error"}})
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resps []rpcResponse
			for i := range msgs {
				if r := s.handle(ctx, &msgs[i]); r != nil {
					resps = append(resps, *r)
				}
			}
			if len(resps) > 0 {
				s.write(resps)
			}
		}()
		return
	}
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		s.write(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{errParse, "parse error: " + err.Error()}})
		return
	}
	if msg.Method == "notifications/cancelled" {
		s.cancel(msg.Params) // 同步处理：要赶在被取消的请求跑完之前
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if r := s.handle(ctx, &msg); r != nil {
			s.write(*r)
		}
	}()
}

// handle 处理一条消息；notification 和客户端发来的 response 返回 nil（不回）。
func (s *Server) handle(ctx context.Context, msg *rpcMessage) *rpcResponse {
	isNotification := len(msg.ID) == 0 || string(msg.ID) == "null"
	if msg.Method == "" || isNotification {
		// 我们从不向客户端发请求，收到的 response 无从对应；通知（initialized 等）无需处理
		if msg.Method == "notifications/cancelled" {
			s.cancel(msg.Params)
		}
		return nil
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: msg.ID}
	if msg.JSONRPC != "2.0" {
		resp.Error = &rpcError{errInvalidRequest, "jsonrpc must be '2.0'"}
		return resp
	}

	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		resp.Result = map[string]any{
			"protocolVersion": negotiateProtocolVersion(p.ProtocolVersion),
			"serverInfo":      map[string]any{"name": "tinia-file", "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolDefs}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil || p.Name == "" {
			resp.Error = &rpcError{errInvalidParams, "tools/call 需要 name"}
			return resp
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		cctx, cancel := context.WithCancel(ctx)
		key := string(msg.ID)
		s.inflightMu.Lock()
		s.inflight[key] = cancel
		s.inflightMu.Unlock()
		resp.Result = s.callTool(cctx, p.Name, p.Arguments)
		s.inflightMu.Lock()
		delete(s.inflight, key)
		s.inflightMu.Unlock()
		cancel()
	default:
		resp.Error = &rpcError{errMethodNotFound, "method not found: " + msg.Method}
	}
	return resp
}

// cancel 处理 notifications/cancelled：取消对应 id 的进行中请求（上传/下载随之中断）。
func (s *Server) cancel(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	s.inflightMu.Lock()
	cancel := s.inflight[string(p.RequestID)]
	s.inflightMu.Unlock()
	if cancel != nil {
		logf("客户端取消了请求 %s", p.RequestID)
		cancel()
	}
}

// write 一条消息一行写到 stdout。encoding/json 输出不含换行（字符串里的换行被转义），
// 满足 stdio 传输「消息内不得有换行」的要求。
func (s *Server) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		logf("响应编码失败: %v", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	if err := s.out.Flush(); err != nil {
		logf("写 stdout 失败: %v", err)
	}
}
