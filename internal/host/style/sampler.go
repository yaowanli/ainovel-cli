package style

// maxSourceRunes 是单篇语料送进模型的上限。
//
// 与 sim 的 60000 头尾拼接不同：这里只取头部。风格 skill 关心的是"开篇怎么
// 立住叙述腔调和人物说话方式"，而这两件事在小说开头最集中；头尾拼接反而把
// 结尾的收束腔调混进来，稀释信号。
const maxSourceRunes = 50000

// sampleSourceContent 截取语料头部最多 50000 字。
func sampleSourceContent(s string) string {
	runes := []rune(s)
	if len(runes) <= maxSourceRunes {
		return s
	}
	return string(runes[:maxSourceRunes])
}
