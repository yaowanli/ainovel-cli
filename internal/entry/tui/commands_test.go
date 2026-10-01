package tui

import "testing"

// /resume 必须在命令表里注册：Esc 暂停后此前没有任何会话内恢复入口。
// Resume() 只在启动、/import 后、/reopen 后被调，而 /reopen 要求 phase=complete、
// /import 只在导入后触发——对「暂停一本进行中的书」都不可用，等于暂停即重启。
func TestResumeCommandRegistered(t *testing.T) {
	spec, ok := commandRegistryInstance().Find("resume")
	if !ok {
		t.Fatal("命令表缺少 /resume")
	}
	if !spec.NeedsIdle {
		t.Error("/resume 应要求空闲态，避免与运行中的引擎抢占")
	}
	if spec.Usage != "/resume" {
		t.Errorf("用法应为 /resume（无参数），实际 %q", spec.Usage)
	}
}

// 恢复命令必须在「写作」分组里可见，否则用户只能靠记忆输入。
func TestResumeCommandVisibleInWritingGroup(t *testing.T) {
	var found bool
	for _, s := range commandRegistryInstance().Visible() {
		if s.Name == "resume" {
			found = true
			if s.Group != "writing" {
				t.Errorf("/resume 应归入 writing 分组，实际 %q", s.Group)
			}
		}
	}
	if !found {
		t.Error("/resume 不应在可见命令里被隐藏")
	}
}
