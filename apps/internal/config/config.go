// Package config 负责读取和校验 MiniCode 的 TOML 配置。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	// FilePath 是相对当前工作区的首选配置路径。
	FilePath       = "config/config.toml"
	legacyFilePath = "apps/config/config.toml"
)

// Config 包含模型服务和可选的 Langfuse 配置。
type Config struct {
	Model struct {
		Provider string `toml:"provider"`
		APIKey   string `toml:"api_key"`
		BaseURL  string `toml:"base_url"`
		Name     string `toml:"name"`
	} `toml:"model"`
	Langfuse struct {
		PublicKey string `toml:"public_key"`
		SecretKey string `toml:"secret_key"`
		Host      string `toml:"host"`
	} `toml:"langfuse"`
}

// Load 从当前工作区读取配置，仅首选文件不存在时兼容旧目录布局。
func Load() (Config, error) {
	var cfg Config
	path := FilePath
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 已存在的断链也属于读取错误，不能被旧配置掩盖。
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			path = legacyFilePath
			data, err = os.ReadFile(path)
		}
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		// 解析错误可能包含密钥所在的原始行，不直接输出错误内容。
		return cfg, fmt.Errorf("cannot parse %s; check TOML syntax, field names and types", path)
	}
	var missing []string
	if strings.TrimSpace(cfg.Model.APIKey) == "" {
		missing = append(missing, "model.api_key")
	}
	if strings.TrimSpace(cfg.Model.BaseURL) == "" {
		missing = append(missing, "model.base_url")
	}
	if strings.TrimSpace(cfg.Model.Name) == "" {
		missing = append(missing, "model.name")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
