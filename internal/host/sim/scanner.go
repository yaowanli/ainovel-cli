package sim

import (
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/corpus"
)

// scannedSource 保留 sim 侧的领域类型嵌入：runner 只关心 SimulationSource
// 的字段，content 由 corpus.Source 提供。原先还有个 absPath 字段，但全包无人
// 读取，随抽包一并删除。
type scannedSource struct {
	domain.SimulationSource
	content string
}

// scanSources 委托给共享的 corpus.Scan——扫描规则（扩展名白名单、GBK 兜底、
// 原始字节指纹）此前只在这里实现，风格 skill 需要同一套行为。
func scanSources(root string) ([]scannedSource, error) {
	sources, err := corpus.Scan(root)
	if err != nil {
		return nil, err
	}
	out := make([]scannedSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, scannedSource{
			SimulationSource: domain.SimulationSource{
				RelativePath: source.RelativePath,
				SHA256:       source.SHA256,
				Fingerprint:  source.Fingerprint,
				SizeBytes:    source.SizeBytes,
				ModTime:      source.ModTime,
			},
			content: source.Content,
		})
	}
	return out, nil
}
