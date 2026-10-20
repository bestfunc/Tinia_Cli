package mcpfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// toolDefs tools/list 的内容。前两个与 Node 版 tinia-file-mcp 的名字、参数完全一致
// （skill 文档按名字引用它们），描述按 CLI 版的实际行为改写。
var toolDefs = []map[string]any{
	{
		"name": "upload_file_to_datasource",
		"description": "【大文件直传·任意大小】上传 AI 运行机器上的本地文件到 Tinia 数据源。文件经 HTTP multipart 流式直传 Tinia Server 的 /api/v1/mcp_data/datasources/:id/uploads → blob 存储，AI context 只过短 JSON，与文件大小无关。" +
			"\n\n适用：" +
			"\n- 用户给了本机 NVH 录音（几百 MB / GB 级 WAV），让 AI 帮上传到指定数据源" +
			"\n- AI 用 Bash + Python 在本机生成测试 wav 写到 /tmp 后传路径" +
			"\n\n鉴权与 Tinia 远程 MCP 同一套 OAuth：第一次调用会打开浏览器授权（授权链接同时打在 stderr），token 存 ~/.tinia/auth.json，与 tinia login 共用。需要「数据源（写入）」权限（mcp:data_write）。",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"datasource_id": map[string]any{"type": "integer", "description": "目标数据源 id（从 Tinia 远程 MCP datasource_list 拿）"},
				"local_path":    map[string]any{"type": "string", "description": "本地文件绝对路径（AI 运行机器上的文件，server 不直接读，文件经本工具 multipart 上传）"},
				"filename":      map[string]any{"type": "string", "description": "可选：远端存储的文件名；不传时自动用 local_path 末端 basename"},
			},
			"required": []string{"datasource_id", "local_path"},
		},
	},
	{
		"name": "fetch_blob",
		"description": "【拉 blob 原始字节到本机】把 Tinia 的 blob 下载到 AI 运行机器的磁盘，返回落地路径 + 格式嗅探 + sha256，供后续用 Python/工具解码读取。" +
			"\n\n典型用途：flow_node_output_preview 只返回 FeatureMatrix / 频谱矩阵端口的「信封」（每行带 local_uri），真实数值矩阵在 local_uri 指向的内层 blob（numpy .npz）里。用本工具把该 local_uri 拉下来，再 np.load 读 fbank / mod_freqs 等数组，导出 CSV。" +
			"\n\n返回 saved_path（默认落系统临时目录），AI context 只过短 JSON，与 blob 大小无关。format 会嗅探 npz/npy/wav/parquet/json 并给一句解码提示。" +
			"\n\n经 Tinia Server 数据面代理读取（鉴权沿用 OAuth access_token，跟大文件上传同一套）；客户端无需对象存储凭据或网络可达性 —— 远程 server 部署、桌面单机版本地存储都通。",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"blob_uri":       map[string]any{"type": "string", "description": "blob 的 uri，形如 minio://tinia-blobs/blobs/70/5c0b...（从 flow_node_output_preview 的 local_uri / blob_uri 字段拿）"},
				"save_path":      map[string]any{"type": "string", "description": "可选：落地绝对路径；不传时落到系统临时目录，文件名 tinia-blob-<hash>.<ext>"},
				"include_base64": map[string]any{"type": "boolean", "description": "可选：文件 ≤64KB 时在返回里附带 base64（默认 false；二进制 blob 一般不需要，直接读 saved_path 即可）"},
			},
			"required": []string{"blob_uri"},
		},
	},
	{
		"name":        "agentport_status",
		"description": "报告本地 MCP 程序（tinia-local-mcp / tinia 命令行）的版本，以及它连接的 Tinia 实例的地址、应用版本、部署形态。用于核对本地安装是否与 Tinia 版本匹配（AgentPort 版本自检）；不需要登录。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// callTool 执行一个工具，返回 MCP 的 CallToolResult。
// 工具自己的错误（参数不对、网络、权限）放进 isError 结果里给模型看，不当协议错误。
func (s *Server) callTool(ctx context.Context, name string, args map[string]any) map[string]any {
	var (
		out any
		err error
	)
	switch name {
	case "upload_file_to_datasource":
		out, err = s.toolUpload(ctx, args)
	case "fetch_blob":
		out, err = s.toolFetchBlob(ctx, args)
	case "agentport_status":
		out, err = s.toolStatus(ctx)
	default:
		err = fmt.Errorf("未知工具: %s", name)
	}
	if err != nil {
		logf("工具 %s 执行失败: %v", name, err)
		return toolText("错误: "+err.Error(), true)
	}
	text, err := indentJSON(out)
	if err != nil {
		return toolText("错误: "+err.Error(), true)
	}
	return toolText(text, false)
}

func toolText(text string, isError bool) map[string]any {
	r := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isError {
		r["isError"] = true
	}
	return r
}

// indentJSON 两空格缩进、不转义 <>&（与 Node 版 JSON.stringify(x, null, 2) 输出一致）。
func indentJSON(v any) (string, error) {
	if raw, ok := v.(json.RawMessage); ok {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

func (s *Server) toolUpload(ctx context.Context, args map[string]any) (any, error) {
	id, ok := args["datasource_id"].(float64)
	if !ok || id <= 0 || id != math.Trunc(id) {
		return nil, errors.New("datasource_id 必填（正整数）")
	}
	localPath, _ := args["local_path"].(string)
	if localPath == "" {
		return nil, errors.New("local_path 必填（string，绝对路径）")
	}
	filename, _ := args["filename"].(string)

	var res any
	err := s.sess.withAuth(ctx, func(c credential) error {
		// 上传是写操作，要 mcp:data_write。用户组只开了「数据源（浏览）」时 token 里没有 ——
		// 给清晰提示，而不是让 server 返回裸 403。scope 未知（TINIA_TOKEN / 桌面版）时交给 server 判。
		if c.Scopes != nil && !hasScope(c.Scopes, ScopeDataWrite) {
			return errors.New("上传文件需要「数据源（写入）」权限（mcp:data_write），当前授权未包含。请联系管理员在用户组模版里勾选「数据源（写入）」后重新授权。")
		}
		r, err := uploadFile(ctx, s.sess.HTTP, s.sess.DataBase(), c, int64(id), localPath, filename)
		if err == nil {
			res = r
		}
		return err
	})
	return res, err
}

func (s *Server) toolFetchBlob(ctx context.Context, args map[string]any) (any, error) {
	blobURI, _ := args["blob_uri"].(string)
	if blobURI == "" {
		return nil, errors.New("blob_uri 必填（string，blob uri）")
	}
	savePath, _ := args["save_path"].(string)
	includeB64, _ := args["include_base64"].(bool)

	var res any
	err := s.sess.withAuth(ctx, func(c credential) error {
		r, err := fetchBlob(ctx, s.sess.HTTP, s.sess.DataBase(), c, blobURI, savePath, includeB64)
		if err == nil {
			res = r
		}
		return err
	})
	return res, err
}

// statusResult agentport_status 的返回。app_* 取自公开的 GET /api/v1/meta。
type statusResult struct {
	Product      string `json:"product"`
	LocalTool    string `json:"local_tool"`
	CLIVersion   string `json:"cli_version"`
	Host         string `json:"host"`
	AppReachable bool   `json:"app_reachable"`
	AppVersion   string `json:"app_version,omitempty"`
	AppEdition   string `json:"app_edition,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (s *Server) toolStatus(ctx context.Context) (any, error) {
	r := statusResult{Product: "tinia", LocalTool: "tinia-file", CLIVersion: s.version, Host: s.sess.Host}
	m, err := s.sess.fetchMeta(ctx, 5*time.Second)
	if err != nil {
		r.Error = fmt.Sprintf("连不上 %s/api/v1/meta: %v", s.sess.Host, err)
		return r, nil
	}
	r.AppReachable = true
	r.AppVersion = m.Version
	r.AppEdition = m.Edition
	return r, nil
}
