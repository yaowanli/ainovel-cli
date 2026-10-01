package tui

import "strings"

type commandRegistry struct {
	specs []slashCommandSpec
}

func newCommandRegistry(specs []slashCommandSpec) commandRegistry {
	return commandRegistry{specs: append([]slashCommandSpec(nil), specs...)}
}

func (r commandRegistry) Visible() []slashCommandSpec {
	var out []slashCommandSpec
	for _, spec := range r.specs {
		if !spec.Hidden {
			out = append(out, spec)
		}
	}
	return out
}

func (r commandRegistry) Find(name string) (slashCommandSpec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return slashCommandSpec{}, false
	}
	for _, spec := range r.specs {
		if spec.matches(name) {
			return spec, true
		}
	}
	return slashCommandSpec{}, false
}

// Suggest 返回与 name 最接近的命令名（编辑距离 ≤ 2，且优于任何已有命令）。
//
// 「未知命令」本身不告诉用户该怎么改，而打错字是未知命令最主要的原因
// （rulse→rules、rewek→rework、staus→status）。给出猜测比让用户去翻命令列表
// 省事得多。距离阈值取 2：够覆盖换位与漏字，又不至于把无关输入也拽上来。
func (r commandRegistry) Suggest(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	best, bestDist := "", maxSuggestDistance+1
	for _, spec := range r.specs {
		d := editDistance(name, spec.Name)
		if d < bestDist {
			best, bestDist = spec.Name, d
		}
		for _, alias := range spec.Aliases {
			if ad := editDistance(name, alias); ad < bestDist {
				best, bestDist = alias, ad
			}
		}
	}
	if bestDist > maxSuggestDistance {
		return ""
	}
	return best
}

// maxSuggestDistance 是允许给出拼写建议的最大编辑距离。
const maxSuggestDistance = 2

// editDistance 是 Levenshtein 距离（迭代双行 DP，空间 O(min(m,n))）。
// 命令名都很短，朴素实现足够；不引入第三方库。
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func (r commandRegistry) PaletteItems() []commandPaletteItem {
	var items []commandPaletteItem
	for _, spec := range r.Visible() {
		items = append(items, commandPaletteItem{
			Name:        spec.Name,
			Aliases:     append([]string(nil), spec.Aliases...),
			Usage:       spec.Usage,
			Description: spec.Description,
			AutoExecute: spec.AutoExecute,
		})
	}
	return items
}
