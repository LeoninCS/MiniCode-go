package memory

import (
	"path/filepath"
	"strings"
)

// DetailDir 保存按需读取的专题正文，简短目录由 MiniCode.md 承载。
const DetailDir = ".minicode/memory"

// IsDetailPath 识别工作区内详情目录下的 Markdown 文件，不扫描或读取目录。
func IsDetailPath(path string) bool {
	path = filepath.Clean(path)
	return filepath.IsLocal(path) &&
		strings.HasPrefix(path, filepath.FromSlash(DetailDir)+string(filepath.Separator)) &&
		filepath.Ext(path) == ".md"
}

// ValidateFile 校验文件工具的完整候选记忆，其他工作区文件沿用工具原有规则。
// 详情不常驻注入，只有根目录 MiniCode.md 受 DefaultTokenBudget 限制。
func ValidateFile(path, content string) error {
	path = filepath.Clean(path)
	if path == FileName {
		_, err := NewDocument(content, DefaultTokenBudget)
		return err
	}
	if IsDetailPath(path) {
		return validateContent(path, content)
	}
	return nil
}
