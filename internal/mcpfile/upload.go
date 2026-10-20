package mcpfile

// 上传本地文件到 Tinia 数据源 —— multipart/form-data 直传 server 的 MCP 数据面
// POST /api/v1/mcp_data/datasources/:id/uploads（不走业务 API，鉴权与权限完全沿用
// mcp.Auth：同一个 OAuth access_token + scope mcp:data_write）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

// uploadFile 上传 localPath 到数据源 datasourceID，返回 server 回的 data（原样透传）。
//
// **流式**：请求体 = multipart 头 + 文件 + 结尾边界，用 io.MultiReader 串起来，
// 文件内容边读边发，内存与文件大小无关（Node 版是 readFile 整份读进内存，GB 级文件就是 GB 级内存）。
// 三段长度都已知，所以照样给出准确的 Content-Length，不走 chunked —— 有的反向代理
// 对 chunked 上传缓冲或拒收，带长度兼容面最大。
func uploadFile(ctx context.Context, hc *http.Client, baseURL string, cred credential, datasourceID int64, localPath, filename string) (json.RawMessage, error) {
	st, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("不是文件: %s", localPath)
	}
	if filename == "" {
		filename = filepath.Base(localPath)
	}
	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	logf("upload %s (%d bytes) → datasource %d", localPath, st.Size(), datasourceID)

	var head, tail bytes.Buffer
	mw := multipart.NewWriter(&head)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename="%s"`, escapeQuotes(filename)))
	h.Set("Content-Type", "application/octet-stream")
	if _, err := mw.CreatePart(h); err != nil {
		return nil, err
	}
	// 结尾边界写到另一块缓冲：multipart.Writer 只往构造时给的 writer 写，
	// 所以临时把它指到 tail 上收 Close() 的输出
	tw := multipart.NewWriter(&tail)
	if err := tw.SetBoundary(mw.Boundary()); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	// tw.Close() 写的是 "\r\n--boundary--\r\n"；首个 part 前没有 \r\n，正好接在文件内容后面

	body := io.MultiReader(&head, f, &tail)
	u := fmt.Sprintf("%s/api/v1/mcp_data/datasources/%d/uploads", baseURL, datasourceID)
	req, err := http.NewRequestWithContext(ctx, "POST", u, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(head.Len()) + st.Size() + int64(tail.Len())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	setAuthHeader(req, cred)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上传失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("上传失败: HTTP %d — %s", resp.StatusCode, respBody)
	}

	// server 用 response.OK 包了一层 { code, data, message }
	var env struct {
		Code    *int            `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("上传响应不是 JSON: %s", respBody)
	}
	if env.Code != nil && *env.Code != 0 {
		msg := env.Message
		if msg == "" {
			msg = string(respBody)
		}
		return nil, fmt.Errorf("上传被拒：%s", msg)
	}
	if len(env.Data) > 0 && string(env.Data) != "null" {
		return env.Data, nil
	}
	return respBody, nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }
