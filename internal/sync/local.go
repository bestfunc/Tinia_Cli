// 本地文件扫描：遍历 workspace 收集 (relPath → sha256 + size)。
// relPath 用 POSIX 分隔（统一与远端 tree 对齐）。

package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// LocalFile 本地一个文件的元信息。
type LocalFile struct {
	RelPath string // 相对 workspace 的 POSIX 路径
	Size    int64
	SHA256  string // hex
}

// ScanLocal 扫 workspace，按 matcher 过滤，返回所有文件（不含目录）。
func ScanLocal(workspace string, matcher *Matcher) ([]LocalFile, error) {
	var out []LocalFile
	err := filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// 用 POSIX 分隔规整
		relPosix := filepath.ToSlash(rel)
		if matcher.Ignored(relPosix) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		out = append(out, LocalFile{
			RelPath: relPosix,
			Size:    info.Size(),
			SHA256:  hash,
		})
		return nil
	})
	return out, err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
