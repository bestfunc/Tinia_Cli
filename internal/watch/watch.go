// Watch 模式：递归监听 workspace 文件变化，debounce 200ms 后触发回调。
//
// 实现要点：
//   - fsnotify 不递归，要手动 Walk + Add 每个目录
//   - 新建子目录时要动态 Add
//   - 排除 matcher 命中的目录直接 SkipDir，省 watcher 开销
//   - debounce：连续多个事件合并成一次回调（IDE 保存常常触发多次）

package watch

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	syncpkg "github.com/bestfunc/tinia-cli/internal/sync"
)

// Run 阻塞运行 watch 循环，文件变化 debounce 后调 onChange。
//
// onChange 收到一组变更过的路径（已用 POSIX 分隔，相对 workspace），
// 内部决定要 push / reload 等。
func Run(ctx context.Context, workspace string, matcher *syncpkg.Matcher, debounce time.Duration, onChange func(changedPaths []string) error) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	// 递归 Add 所有目录
	if err := addRecursive(w, workspace, workspace, matcher); err != nil {
		return err
	}

	// debounce 缓冲
	pending := map[string]struct{}{}
	timer := time.NewTimer(debounce)
	timer.Stop()

	flush := func() {
		if len(pending) == 0 {
			return
		}
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		pending = map[string]struct{}{}
		if err := onChange(paths); err != nil {
			// 不退出 watch，错误打印让 caller 处理（caller 在 onChange 里直接 fmt.Println 报错）
			_ = err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			rel, err := filepath.Rel(workspace, ev.Name)
			if err != nil {
				continue
			}
			relPosix := filepath.ToSlash(rel)
			if matcher.Ignored(relPosix) {
				continue
			}
			// 新建目录 → 动态 Add
			if ev.Op&fsnotify.Create == fsnotify.Create {
				info, err := os.Stat(ev.Name)
				if err == nil && info.IsDir() {
					_ = addRecursive(w, workspace, ev.Name, matcher)
				}
			}
			pending[relPosix] = struct{}{}
			timer.Reset(debounce)
		case <-timer.C:
			flush()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			_ = err // 不致命，继续
		}
	}
}

func addRecursive(w *fsnotify.Watcher, workspace, root string, matcher *syncpkg.Matcher) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		relPosix := filepath.ToSlash(rel)
		if rel == "." {
			return w.Add(path)
		}
		if matcher.Ignored(relPosix) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}
