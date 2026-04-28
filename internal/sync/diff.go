// Diff 决策：把（本地文件清单 + 上次快照 + 远端文件清单）算成"要做的动作"。
//
// 动作三种：upload（新增/修改）/ delete（远端有本地无）/ skip（无变化）。
//
// upload 的判断：
//   - 本地有，但 lastsync 没记录 sha 或 sha 跟当前不同 → upload
//   - lastsync 记了同 sha，但远端没这文件（dev_tree 没出现）→ upload（容灾：远端被人删过）
//
// delete 的判断（仅本地有 lastsync 记录的文件才被认为是"我推过的"，避免误删别人在 Web Studio 创建的）：
//   - lastsync 有该 path，本地没有，远端有 → delete

package sync

import (
	"sort"
)

// Action 单个文件的同步动作。
type Action struct {
	Op      string // "upload" | "delete"
	RelPath string
	SHA256  string // upload 时填本地新 sha；delete 时空
}

// DiffPush 计算 push 时要做的动作列表。
//
// 入参：
//   - locals     本地扫描结果
//   - remote     远端文件清单（dev_tree flatten）
//   - lastSync   上次推送的快照（path → sha）
//
// 返回的 actions 已按 path 排序，便于稳定输出。
func DiffPush(locals []LocalFile, remote []string, lastSync *LastSync) []Action {
	remoteSet := make(map[string]bool, len(remote))
	for _, p := range remote {
		remoteSet[p] = true
	}
	localMap := make(map[string]LocalFile, len(locals))
	for _, f := range locals {
		localMap[f.RelPath] = f
	}

	var actions []Action

	// 先扫本地：upload（新增 / 修改 / 远端没了的容灾）
	for _, f := range locals {
		oldSHA, hadLastSync := lastSync.Files[f.RelPath]
		needUpload := false
		switch {
		case !hadLastSync:
			needUpload = true // 全新
		case oldSHA != f.SHA256:
			needUpload = true // 本地变了
		case !remoteSet[f.RelPath]:
			needUpload = true // 远端没了，容灾再传一次
		}
		if needUpload {
			actions = append(actions, Action{Op: "upload", RelPath: f.RelPath, SHA256: f.SHA256})
		}
	}

	// 再扫 lastsync：delete（之前推过但本地删了，且远端确实存在）
	for path := range lastSync.Files {
		if _, stillLocal := localMap[path]; stillLocal {
			continue
		}
		if !remoteSet[path] {
			continue // 远端也已经没了，无需 delete
		}
		actions = append(actions, Action{Op: "delete", RelPath: path})
	}

	sort.Slice(actions, func(i, j int) bool {
		if actions[i].Op != actions[j].Op {
			// upload 排前面，delete 后面（更直观）
			return actions[i].Op < actions[j].Op
		}
		return actions[i].RelPath < actions[j].RelPath
	})
	return actions
}
