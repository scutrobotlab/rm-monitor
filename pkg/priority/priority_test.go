package priority

import (
	"testing"

	common "scutbot.cn/web/rm-monitor/pkg/config"
)

func TestForSchools(t *testing.T) {
	items := []common.PriorityItem{
		{School: "华南理工大学", Priority: 100},
		{School: "华南理工大学", Priority: 120},
		{School: "上海交通大学", Priority: 80},
	}
	if got := ForSchools(nil, "华南理工大学"); got != 0 {
		t.Fatalf("empty priority config = %d, want 0", got)
	}
	if got := ForSchools(items, "其他学校", "上海交通大学"); got != 80 {
		t.Fatalf("blue-side school priority = %d, want 80", got)
	}
	if got := ForSchools(items, "华南理工大学", "上海交通大学"); got != 120 {
		t.Fatalf("max matched priority = %d, want 120", got)
	}
	if got := ForSchools(items, "华南理工大学 "); got != 120 {
		t.Fatalf("trimmed school priority = %d, want 120", got)
	}
}
