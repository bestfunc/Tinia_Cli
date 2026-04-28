// .tinia/lastsync.json 记录上次同步时各文件的 sha256 + 远端是否存在，
// 让 push 走增量（同 sha = 跳过）。
//
// 不要求强一致 —— 文件丢了或者损坏，整个就当作"全部要重推"，慢但不会出错。

package sync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// LastSync 上次同步快照（按 host + project_id 区分一份）。
type LastSync struct {
	// path → sha256
	Files map[string]string `json:"files"`
}

func lastSyncPath(workspace string) string {
	return filepath.Join(workspace, ".tinia", "lastsync.json")
}

// LoadLastSync 读上次快照；不存在返回空 map。
func LoadLastSync(workspace string) (*LastSync, error) {
	data, err := os.ReadFile(lastSyncPath(workspace))
	if errors.Is(err, os.ErrNotExist) {
		return &LastSync{Files: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var ls LastSync
	if err := json.Unmarshal(data, &ls); err != nil {
		// 损坏 → 当作空（下次 push 会全推）
		return &LastSync{Files: map[string]string{}}, nil
	}
	if ls.Files == nil {
		ls.Files = map[string]string{}
	}
	return &ls, nil
}

// Save 写回（原子）。
func (ls *LastSync) Save(workspace string) error {
	dir := filepath.Join(workspace, ".tinia")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return err
	}
	tmp := lastSyncPath(workspace) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, lastSyncPath(workspace))
}
