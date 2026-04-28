// 项目级配置在 <workspace>/.tinia/config.yaml：
//
//	host: https://tinia-saas.bestfunc.com
//	project_id: 42
//	project_name: my-acoustic-tools
//	# 可选：同步时排除的额外 glob（默认含 .git / node_modules / __pycache__ / .venv 等）
//	exclude:
//	  - "tmp/**"
//	  - "*.log"
//
// 同时存在 .tinia/lastsync.json 记录上次 push 各文件的 sha256，让下次 push 走增量。

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	dirName    = ".tinia"
	configFile = "config.yaml"
)

// Config 项目级配置。
type Config struct {
	Host        string   `yaml:"host"`
	ProjectID   int      `yaml:"project_id"`
	ProjectName string   `yaml:"project_name,omitempty"`
	Namespace   string   `yaml:"namespace,omitempty"`
	Exclude     []string `yaml:"exclude,omitempty"`

	// 缓存：所属 workspace 绝对路径（运行时填，不写盘）
	WorkspaceDir string `yaml:"-"`
}

// Path 返回项目 config 文件的绝对路径。
func Path(workspace string) string {
	return filepath.Join(workspace, dirName, configFile)
}

// Load 从 workspace（默认当前目录）加载配置。沿目录树向上找直到根。
func Load() (*Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir := cwd
	for {
		path := Path(dir)
		if _, err := os.Stat(path); err == nil {
			return loadFile(path, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, errors.New("当前目录及上层都没找到 .tinia/config.yaml — 请先 tinia init")
		}
		dir = parent
	}
}

func loadFile(path, workspace string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config.yaml 解析失败: %w", err)
	}
	cfg.WorkspaceDir = workspace
	if cfg.Host == "" || cfg.ProjectID == 0 {
		return nil, fmt.Errorf("config.yaml 缺少 host 或 project_id")
	}
	return &cfg, nil
}

// Save 保存到 workspace（init / migrate 用）。
func Save(workspace string, cfg *Config) error {
	dir := filepath.Join(workspace, dirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := []byte("# Tinia CLI 项目配置 — 生成于 tinia init，版本受控（可提交）\n")
	return os.WriteFile(filepath.Join(dir, configFile), append(header, data...), 0644)
}
