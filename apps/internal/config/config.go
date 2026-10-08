// Package config 负责读取和校验 MiniCode 的 TOML 配置。
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// FilePath 是模型和 Langfuse 配置的唯一文件路径。
const FilePath = "apps/config/config.toml"

// Config 包含模型服务和可选的 Langfuse 配置。
type Config struct {
	Model struct {
		APIKey  string `toml:"api_key"`
		BaseURL string `toml:"base_url"`
		Name    string `toml:"name"`
	} `toml:"model"`
	Langfuse struct {
		PublicKey string `toml:"public_key"`
		SecretKey string `toml:"secret_key"`
		Host      string `toml:"host"`
	} `toml:"langfuse"`
}

// Load 从固定路径读取模型和 Langfuse 配置。
func Load() (Config, error) {
	var cfg Config
	data, err := os.ReadFile(FilePath)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		// 解析错误可能包含密钥所在的原始行，不直接输出错误内容。
		return cfg, fmt.Errorf("cannot parse %s; check TOML syntax, field names and types", FilePath)
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
