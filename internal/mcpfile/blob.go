package mcpfile

// 把 Tinia 的 blob 拉到本机磁盘，供 AI 后续用 Python/工具解码读取。
//
// 走 Tinia Server 的数据面 blob 代理（GET /api/v1/mcp_data/blob-proxy?uri=...），
// server 端经 blob Store 读 MinIO / 本地 fs，客户端不需要对象存储凭据或网络可达性 ——
// 远程 server 部署、桌面单机版本地存储都通。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// fetchBlobResult 字段名与顺序与 Node 版一致。
type fetchBlobResult struct {
	BlobURI    string `json:"blob_uri"`
	SizeBytes  int64  `json:"size_bytes"`
	Format     string `json:"format"`      // npz / npy / json / wav / parquet / png / unknown
	FormatHint string `json:"format_hint"` // 一句话解码提示（给 AI 看）
	SHA256     string `json:"sha256"`
	SavedPath  string `json:"saved_path"`
	Base64     string `json:"base64,omitempty"` // 仅 include_base64 且不超过内联上限时给
}

const inlineLimit = 64 * 1024 // include_base64 时的内联上限

// sniffFormat 按魔术字节嗅探格式，给 AI 一句解码提示。
func sniffFormat(b []byte) (format, hint string) {
	switch {
	case len(b) >= 4 && b[0] == 0x50 && b[1] == 0x4b && b[2] == 0x03 && b[3] == 0x04:
		// PK\x03\x04 —— zip。Tinia 的 FeatureMatrix / 频谱矩阵都是 np.savez_compressed 出来的 .npz
		return "npz", "numpy .npz（zip）。Python: import numpy as np; z=np.load(path); z.files 列数组，z['fbank'] 取矩阵。"
	case len(b) >= 6 && b[0] == 0x93 && string(b[1:6]) == "NUMPY":
		return "npy", "numpy .npy 单数组。Python: np.load(path)。"
	case len(b) >= 4 && string(b[:4]) == "RIFF":
		return "wav", "WAV 音频。"
	case len(b) >= 4 && string(b[:4]) == "PAR1":
		return "parquet", "Apache Parquet。Python: pandas.read_parquet(path)。"
	case len(b) >= 8 && b[0] == 0x89 && string(b[1:4]) == "PNG":
		return "png", "PNG 图片。"
	}
	// 第一个非空白字符是 { 或 [ 就当 JSON 文本
	t := bytes.TrimLeft(b, " \t\r\n")
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		return "json", "JSON 文本。"
	}
	return "unknown", "未知二进制，按需自行判断。"
}

// fetchBlob 经数据面代理拉 blob → 流式落盘 → 返回元信息。
//
// 内存恒定在一个 chunk：边下边算 sha256，只留开头 64 字节做嗅探。
// 先写 <目标>.part，校验完再 rename：
//   - 下载中断：留下 .part 供排查（报错里给路径），目标路径不被污染；
//   - Content-Length 对不上：删掉 .part 再报错 —— 少了字节却不报错，是最难查的那种坏数据。
func fetchBlob(ctx context.Context, hc *http.Client, baseURL string, cred credential, blobURI, savePath string, includeBase64 bool) (*fetchBlobResult, error) {
	u := baseURL + "/api/v1/mcp_data/blob-proxy?uri=" + url.QueryEscape(blobURI)
	logf("fetch_blob %s → %s", blobURI, baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	setAuthHeader(req, cred)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blob 读取失败: %w\nuri=%s", err, blobURI)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errUnauthorized
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("blob 读取失败: HTTP %d — %s\nuri=%s\n%s", resp.StatusCode, http.StatusText(resp.StatusCode), blobURI, body)
	}

	declared, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	tmpBase := savePath
	if tmpBase == "" {
		tmpBase = filepath.Join(os.TempDir(), "tinia-blob-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	}
	tmpPath := tmpBase + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("创建落地文件失败: %w", err)
	}

	tap := &blobTap{hasher: sha256.New(), keepInline: includeBase64, declared: declared}
	_, copyErr := io.Copy(f, io.TeeReader(resp.Body, tap))
	closeErr := f.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if errors.Is(copyErr, io.ErrUnexpectedEOF) && declared > 0 && tap.size < declared {
		// 连接在 Content-Length 之前就断了：Go 的 http client 把这种情况报成
		// unexpected EOF。按「长度对不上」处理 —— 删掉半截，别让人误用
		copyErr = nil
	}
	if copyErr != nil {
		// 半成品留在 .part：看得出下到哪断的，也不污染目标路径
		total := ""
		if declared > 0 {
			total = fmt.Sprintf(" / 共 %.1fMB", float64(declared)/1048576)
		}
		return nil, fmt.Errorf("blob 下载中断（已写 %.1fMB%s）: %v\nuri=%s\n半成品: %s",
			float64(tap.size)/1048576, total, copyErr, blobURI, tmpPath)
	}
	if declared > 0 && tap.size != declared {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("blob 下载不完整: 收到 %d 字节，Content-Length 声明 %d\nuri=%s", tap.size, declared, blobURI)
	}

	format, hint := sniffFormat(tap.head)
	sum := hex.EncodeToString(tap.hasher.Sum(nil))
	final := savePath
	if final == "" {
		ext := format
		if ext == "unknown" {
			ext = "bin"
		}
		// 默认落临时目录，文件名带 hash 前缀 + 格式后缀，方便 AI 直接喂给 python
		final = filepath.Join(os.TempDir(), "tinia-blob-"+sum[:16]+"."+ext)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return nil, fmt.Errorf("落盘失败: %w（数据在 %s）", err, tmpPath)
	}

	out := &fetchBlobResult{
		BlobURI: blobURI, SizeBytes: tap.size, Format: format, FormatHint: hint,
		SHA256: sum, SavedPath: final,
	}
	if includeBase64 && tap.size <= inlineLimit {
		out.Base64 = base64.StdEncoding.EncodeToString(tap.inline.Bytes())
	}
	return out, nil
}

// blobTap 挂在下载流上：算 hash、计字节、留首片、（小文件）留全文，大文件隔 32MB 报一次进度。
type blobTap struct {
	hasher     hash.Hash
	size       int64
	declared   int64
	head       []byte
	keepInline bool
	inline     bytes.Buffer
	lastLogged int64
}

func (t *blobTap) Write(p []byte) (int, error) {
	t.hasher.Write(p)
	if len(t.head) < 64 {
		n := 64 - len(t.head)
		if n > len(p) {
			n = len(p)
		}
		t.head = append(t.head, p[:n]...)
	}
	t.size += int64(len(p))
	if t.keepInline && t.size <= inlineLimit {
		t.inline.Write(p)
	}
	// 大文件给点动静，否则 stderr 上看起来像卡死
	if t.size-t.lastLogged >= 32<<20 {
		t.lastLogged = t.size
		pct := ""
		if t.declared > 0 {
			pct = fmt.Sprintf(" (%.0f%%)", float64(t.size)/float64(t.declared)*100)
		}
		logf("fetch_blob 进度 %dMB%s", t.size>>20, pct)
	}
	return len(p), nil
}
