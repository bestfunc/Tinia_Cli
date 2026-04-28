// MCP HTTP 客户端 —— 所有 dev / nodes / flow / plugins 工具都通过 POST
// {host}/api/v1/mcp 调用，JSON-RPC 2.0 格式：
//
//	{ "jsonrpc": "2.0", "id": <int>, "method": "tools/call",
//	  "params": { "name": "<tool>", "arguments": { ... } } }
//
// 响应：result.content[0].text 是 JSON 字符串，需二次解析。
//
// 401 时 caller 自己判断要不要 refresh + 重试（见 internal/auth）。

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Client 一个 host 一个 client。
type Client struct {
	Host        string // e.g. https://tinia-saas.bestfunc.com
	AccessToken string

	HTTP    *http.Client
	nextID  atomic.Int64
}

func New(host, accessToken string) *Client {
	return &Client{
		Host:        strings.TrimRight(host, "/"),
		AccessToken: accessToken,
		HTTP:        &http.Client{Timeout: 60 * time.Second},
	}
}

// jsonrpcReq / jsonrpcResp 是 MCP 协议的 JSON-RPC 2.0 信封。
type jsonrpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// mcpToolResult 是 MCP tools/call 的返回结构。
type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"` // JSON 字符串
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// Call 调一个 MCP tool，把 result.content[0].text 解析回 out（&struct）。
//
// out 可以是 nil（不关心返回值）；也可以是 *map[string]any 拿原始 JSON。
func (c *Client) Call(ctx context.Context, tool string, args any, out any) error {
	id := c.nextID.Add(1)
	body, err := json.Marshal(jsonrpcReq{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      tool,
			"arguments": args,
		},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.Host+"/api/v1/mcp", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 401 {
		return ErrUnauthorized{Body: string(respBody)}
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var rpc jsonrpcResp
	if err := json.Unmarshal(respBody, &rpc); err != nil {
		return fmt.Errorf("解析 JSON-RPC 响应: %w (body: %s)", err, string(respBody))
	}
	if rpc.Error != nil {
		return fmt.Errorf("%s (code=%d)", rpc.Error.Message, rpc.Error.Code)
	}

	var tr mcpToolResult
	if err := json.Unmarshal(rpc.Result, &tr); err != nil {
		return fmt.Errorf("解析 tool result: %w", err)
	}
	if tr.IsError {
		errMsg := "tool 执行失败"
		if len(tr.Content) > 0 {
			errMsg = tr.Content[0].Text
		}
		return fmt.Errorf("%s", errMsg)
	}
	if out == nil || len(tr.Content) == 0 {
		return nil
	}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), out); err != nil {
		return fmt.Errorf("解析 tool 返回 JSON: %w (text: %s)", err, tr.Content[0].Text)
	}
	return nil
}

// ErrUnauthorized 401 — caller 通常需要 refresh token 后重试。
type ErrUnauthorized struct{ Body string }

func (e ErrUnauthorized) Error() string {
	return "未授权（access_token 过期或失效）: " + e.Body
}
