// 忽略规则：默认排除 + 用户在 .tinia/config.yaml 加的额外 glob。
//
// 默认列表跟后端 buildTree 保持一致（.git / __pycache__ / .venv / node_modules）+ CLI 自己的
// .tinia/ 目录（避免把 lastsync.json / config.yaml 推回去）。
//
// 匹配语法用 doublestar（gitignore 风格 ** 通配），比 stdlib filepath.Match 更强。

package sync

import (
	"path/filepath"
	"strings"
)

// 默认排除（任何路径段命中 = 整条路径忽略）
var defaultIgnoreSegments = []string{
	".git",
	".tinia",
	"__pycache__",
	".venv",
	"venv",
	"node_modules",
	".DS_Store",
	".idea",
	".vscode",
	"dist",
	"build",
	".build", // dev workspace UI 编译产物（避免把 server 端构建产物再推回去）
}

// 默认 glob（通配）
var defaultIgnoreGlobs = []string{
	"*.pyc",
	"*.pyo",
	"*.swp",
	"*.log",
}

// Matcher 决定一个相对路径（relPath，POSIX 分隔）是否被忽略。
type Matcher struct {
	extraGlobs []string
}

// NewMatcher 用 config.exclude 里用户写的 glob 构造。
func NewMatcher(extraGlobs []string) *Matcher {
	// 复制一份避免修改原 slice
	g := make([]string, 0, len(extraGlobs))
	g = append(g, extraGlobs...)
	return &Matcher{extraGlobs: g}
}

// Ignored 判断 relPath（相对 workspace，已用 / 分隔）是否被忽略。
func (m *Matcher) Ignored(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	segs := strings.Split(relPath, "/")

	// 1. 任何段命中默认列表
	for _, seg := range segs {
		for _, ign := range defaultIgnoreSegments {
			if seg == ign {
				return true
			}
		}
	}

	// 2. basename 命中默认 glob
	base := segs[len(segs)-1]
	for _, glob := range defaultIgnoreGlobs {
		if ok, _ := filepath.Match(glob, base); ok {
			return true
		}
	}

	// 3. 用户自定义 glob —— filepath.Match 不支持 **，简化为：
	//    含 / 的 glob 当作完整 relPath 匹配；不含 / 当作 basename 匹配
	for _, glob := range m.extraGlobs {
		target := base
		if strings.Contains(glob, "/") {
			target = relPath
		}
		if ok, _ := filepath.Match(glob, target); ok {
			return true
		}
	}

	return false
}
