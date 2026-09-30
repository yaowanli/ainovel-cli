package flow

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// TestReworkPassEndToEndLoop 模拟引擎逐轮路由，验证 pass 能真正走完一个区间
// 而不是卡死或漏章。每轮按 router 的真实输出决定下一步：editor 评审 → 若入队
// 则 writer 改该章并出队 → 再评下一章。
func TestReworkPassEndToEndLoop(t *testing.T) {
	// 8 章已完成，返工 1-4。被判返工的章：2 与 4（其余评审通过）。
	needsRework := map[int]bool{2: true, 4: true}
	reworked := map[int]bool{}

	state := State{Progress: &domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    9,
		CompletedChapters: []int{1, 2, 3, 4, 5, 6, 7, 8},
		Flow:              domain.FlowWriting,
		ReworkPass:        &domain.ReworkPass{StartChapter: 1, EndChapter: 4, Cursor: 1},
	}}
	// advanceReworkPass 的语义在 store 包内；这里用与 router 相同的推进规则模拟：
	// 评审落盘即 +1，被标记则入队。
	advance := func() {
		p := state.Progress.ReworkPass
		cur := p.Cursor
		p.Reviewed++
		if needsRework[cur] {
			state.Progress.PendingRewrites = append(state.Progress.PendingRewrites, cur)
			p.Rewritten = append(p.Rewritten, cur)
		} else {
			p.Skipped++
		}
		p.Cursor++
	}

	for step := 0; step < 40; step++ {
		inst := Route(state)
		if inst == nil {
			t.Fatalf("step %d: 路由返回 nil，引擎无事可做", step)
		}
		switch inst.Agent {
		case "editor":
			if inst.Chapter != state.Progress.ReworkPass.Cursor {
				t.Fatalf("step %d: editor 被派给第 %d 章，但游标是 %d",
					step, inst.Chapter, state.Progress.ReworkPass.Cursor)
			}
			advance()
		case "writer":
			// 必须是队列头：改完才评下一章，不得交错
			queue := state.Progress.PendingRewrites
			if len(queue) == 0 {
				t.Fatalf("step %d: 派发 writer 但队列为空", step)
			}
			if queue[0] != inst.Chapter {
				t.Fatalf("step %d: writer 目标是第 %d 章，队列头是 %d（不得交错）",
					step, inst.Chapter, queue[0])
			}
			reworked[queue[0]] = true
			state.Progress.PendingRewrites = queue[1:]
			if state.Progress.Flow == domain.FlowRewriting && len(state.Progress.PendingRewrites) == 0 {
				state.Progress.Flow = domain.FlowWriting
			}
		default:
			t.Fatalf("step %d: 意外 agent=%q task=%s", step, inst.Agent, inst.Task)
		}
		if state.Progress.ReworkPass.Done() && len(state.Progress.PendingRewrites) == 0 {
			final := Route(state)
			if final == nil || final.Agent != "writer" || final.Chapter != 9 {
				t.Fatalf("pass 完成且队列排空后应续写第 9 章，实际 %+v", final)
			}
			break
		}
		// pass 的 Done 只表示"所有章已评审"，队列仍须排空才回到续写：
		// 最后一批被判返工的章是在游标越界之后才入队的。
		if step == 39 {
			t.Fatal("40 轮内未跑完 pass，可能死循环")
		}
	}

	pass := state.Progress.ReworkPass
	if pass.Cursor != 5 || !pass.Done() {
		t.Fatalf("pass 应在第 5 轮后完成: %+v", pass)
	}
	if pass.Reviewed != 4 || pass.Skipped != 2 {
		t.Fatalf("应评审 4 章、通过 2 章: %+v", pass)
	}
	if len(pass.Rewritten) != 2 || pass.Rewritten[0] != 2 || pass.Rewritten[1] != 4 {
		t.Fatalf("实际返工章应为 [2 4]: %v", pass.Rewritten)
	}
	if !reworked[2] || !reworked[4] || reworked[1] || reworked[3] {
		t.Fatalf("实际改写集合不符: %v", reworked)
	}
}
