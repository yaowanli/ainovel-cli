package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
)

const reworkUsage = `用法：
  /rework <章号>            返工单章，如 /rework 50
  /rework <起>-<止>          返工一个范围，如 /rework 1-112（会先给出章数与预估，确认后加 --yes）
  /rework status            查看当前返工进度
  /rework stop              中止当前返工（已入队的章仍会改完）
  /rework help              显示本帮助

每章先由 Editor 按当前 anti_ai_tone 与 style_skills 判据评审，确有问题才重写；
评审通过的章直接跳过。返工期间后续已定稿章节会作为约束注入，不会被改坏。`

// parseReworkRange 解析 "50" 或 "1-112"。返回单章时 start==end。
func parseReworkRange(arg string) (start, end int, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, 0, fmt.Errorf("缺少章号或范围")
	}
	if lo, hi, found := strings.Cut(arg, "-"); found {
		start, err = strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return 0, 0, fmt.Errorf("起始章号无法解析：%s", lo)
		}
		end, err = strconv.Atoi(strings.TrimSpace(hi))
		if err != nil {
			return 0, 0, fmt.Errorf("结束章号无法解析：%s", hi)
		}
		return start, end, nil
	}
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, 0, fmt.Errorf("章号无法解析：%s", arg)
	}
	return n, n, nil
}

// formatReworkStatus 渲染 /rework status。pass 为 nil 时给出引导。
func formatReworkStatus(pass *domain.ReworkPass) string {
	if pass == nil {
		return "当前没有返工记录。用 /rework <章号> 或 /rework <起>-<止> 开启。"
	}
	state := "进行中"
	if pass.Done() {
		state = "已完成"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "逐章返工 %s：第 %d-%d 章（共 %d 章）\n", state, pass.StartChapter, pass.EndChapter, pass.Total())
	fmt.Fprintf(&b, "  游标 %d　已评审 %d　已返工 %d　评审通过 %d\n",
		pass.Cursor, pass.Reviewed, len(pass.Rewritten), pass.Skipped)
	if len(pass.Rewritten) > 0 {
		fmt.Fprintf(&b, "  实际返工章：%s\n", formatChapterList(pass.Rewritten))
	} else if pass.Reviewed > 0 {
		b.WriteString("  实际返工章：（暂无）\n")
	}
	if pass.StartedAt != "" {
		fmt.Fprintf(&b, "  开始于 %s\n", pass.StartedAt)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatChapterList 把章号列表压成 "1-3, 7, 12-14" 的紧凑形式，避免 112 章刷屏。
func formatChapterList(chapters []int) string {
	if len(chapters) == 0 {
		return ""
	}
	sorted := append([]int(nil), chapters...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var parts []string
	runStart, runEnd := sorted[0], sorted[0]
	flush := func() {
		if runStart == runEnd {
			parts = append(parts, strconv.Itoa(runStart))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", runStart, runEnd))
		}
	}
	for _, n := range sorted[1:] {
		if n == runEnd+1 {
			runEnd = n
			continue
		}
		flush()
		runStart, runEnd = n, n
	}
	flush()
	return strings.Join(parts, ", ")
}

// runRework 实现 /rework 的分发。返回是否已消费本次命令。
func runRework(m Model, args []string) (Model, bool) {
	if len(args) == 0 {
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: reworkUsage})
		m.refreshEventViewport()
		return m, true
	}
	switch args[0] {
	case "help", "-h", "--help":
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: reworkUsage})
		m.refreshEventViewport()
		return m, true
	case "status":
		pass, err := m.runtime.ReworkStatus()
		if err != nil {
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "读取返工状态失败：" + err.Error()})
			m.refreshEventViewport()
			return m, true
		}
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: formatReworkStatus(pass)})
		m.refreshEventViewport()
		return m, true
	case "stop":
		pass, err := m.runtime.StopReworkPass()
		if err != nil {
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "中止返工失败：" + err.Error()})
			m.refreshEventViewport()
			return m, true
		}
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: fmt.Sprintf("已中止返工 pass（第 %d-%d 章）：已评审 %d 章，返工 %d 章（%s），评审通过 %d 章。%s",
				pass.StartChapter, pass.EndChapter, pass.Reviewed, len(pass.Rewritten),
				formatChapterList(pass.Rewritten), pass.Skipped, "待改章节仍会按队列继续处理。"),
		})
		m.refreshEventViewport()
		return m, true
	}

	// 范围形式。--yes 是显式放行，与 /import --yes 同一约定：先看计划再决定。
	confirmed := false
	rest := args
	if args[len(args)-1] == "--yes" || args[len(args)-1] == "-y" {
		confirmed = true
		rest = args[:len(args)-1]
	}
	start, end, err := parseReworkRange(strings.Join(rest, ""))
	if err != nil {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: err.Error() + "\n" + reworkUsage,
		})
		m.refreshEventViewport()
		return m, true
	}
	plan, err := m.runtime.ReworkPlanFor(start, end)
	if err != nil {
		m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "无法开启返工：" + err.Error()})
		m.refreshEventViewport()
		return m, true
	}
	// 单章直接开：代价可控，不必多一次按键。
	if !confirmed && plan.Total > 1 {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: fmt.Sprintf("将逐章返工第 %d-%d 章，共 %d 章（当前最多可返工到第 %d 章）。\n"+
				"每章 1 次 Editor 评审，确有问题才重写（约 %d 章，按历史返工率估算），每次评审与重写都会调用 LLM。\n"+
				"确认请执行：/rework %d-%d --yes",
				plan.StartChapter, plan.EndChapter, plan.Total, plan.RewritableMax,
				reworkEstimateRewrites(plan.Total), plan.StartChapter, plan.EndChapter),
		})
		m.refreshEventViewport()
		return m, true
	}
	if _, err := m.runtime.StartReworkPass(plan.StartChapter, plan.EndChapter); err != nil {
		m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: "开启返工失败：" + err.Error()})
		m.refreshEventViewport()
		return m, true
	}
	return m, true
}

// reworkEstimateRewrites 按历史返工率粗估实际会被重写的章数，仅用于确认提示。
// 该项目实测 27/112≈24%；这里用同一量级，取保守下限 20%。
func reworkEstimateRewrites(total int) int {
	n := total * 20 / 100
	if n < 1 && total > 0 {
		n = 1
	}
	return n
}
