// Package corpus 扫描参考语料目录，为仿写画像与风格 skill 提供统一的
// 文件发现、编码兜底与内容指纹。
//
// 从 internal/host/sim 抽出：扫描逻辑与仿真业务无关，此前只有 simulate 一个
// 调用方，风格 skill 需要同一套行为。抽包而不是复制，是为了让"指纹算在原始
// 字节上、content 解码后供 LLM 分析"这条约定只有一处实现。
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// Source 是单篇参考语料。Fingerprint 标识文件身份（路径+内容摘要），
// 供两条管道各自做增量去重。
type Source struct {
	RelativePath string
	SHA256       string
	Fingerprint  string
	SizeBytes    int64
	ModTime      string
	Content      string
}

// Fingerprint 返回语料身份指纹。canonical 实现在 domain，这里只做转发，
// 避免两条管道各写一份同名字符串拼接。
func Fingerprint(relativePath, sha256Hex string) string {
	return domain.SourceFingerprint(relativePath, sha256Hex)
}

// Scan 递归扫描 root 下所有受支持的语料文件，按相对路径排序返回。
func Scan(root string) ([]Source, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("source dir is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("corpus directory not found: %s", root)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("corpus path is not a directory: %s", root)
	}

	var out []Source
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !isSupported(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		out = append(out, Source{
			RelativePath: rel,
			SHA256:       sha,
			Fingerprint:  Fingerprint(rel, sha),
			SizeBytes:    fileInfo.Size(),
			ModTime:      fileInfo.ModTime().Format(time.RFC3339),
			// 指纹算在原始字节上（文件身份，增量去重稳定）；content 解码后供
			// LLM 分析——GBK 语料直接当 UTF-8 读是乱码，画像会被静默喂垃圾。
			Content: utils.DecodeText(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RelativePath < out[j].RelativePath
	})
	return out, nil
}

func isSupported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown":
		return true
	default:
		return false
	}
}
