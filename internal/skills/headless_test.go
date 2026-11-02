package skills

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestAgentHeadlessValidation(t *testing.T) {
	home := testHome(t)
	for _, tc := range []struct {
		name, headless, rule string
	}{
		{"blank", `"   "`, "[agent] headless must not be blank"},
		{"tab", `"\t"`, "[agent] headless must not be blank"},
		{"equal", `"agenty --go"`, "must not equal [agent] command"},
		{"equal-padded", `" agenty --go "`, "must not equal [agent] command"},
	} {
		writeSkill(t, home, "h-"+tc.name, `
[agent]
command = "agenty --go"
headless = `+tc.headless+`
`, nil)
		_, err := Resolve(config.Config{Agent: "h-" + tc.name}, catFor(t, home))
		if err == nil || !strings.Contains(err.Error(), tc.rule) {
			t.Errorf("%s: want rule %q, got %v", tc.name, tc.rule, err)
		}
	}

	writeSkill(t, home, "h-ok", `
[agent]
command = "agenty --go"
headless = "agenty-headless --json"
`, nil)
	res, err := Resolve(config.Config{Agent: "h-ok"}, catFor(t, home))
	if err != nil {
		t.Fatalf("valid headless must load: %v", err)
	}
	if got := res.AgentHeadless(); got != "agenty-headless --json" {
		t.Errorf("AgentHeadless = %q", got)
	}

	// A manifest without the field loads unchanged and reports no headless mode.
	writeSkill(t, home, "h-none", `
[agent]
command = "agenty --go"
`, nil)
	res, err = Resolve(config.Config{Agent: "h-none"}, catFor(t, home))
	if err != nil {
		t.Fatalf("manifest without headless must load: %v", err)
	}
	if res.AgentHeadless() != "" || res.AgentCommand() != "agenty --go" {
		t.Errorf("no-headless agent: headless=%q command=%q", res.AgentHeadless(), res.AgentCommand())
	}
	if (Resolved{}).AgentHeadless() != "" {
		t.Error("no agent should mean empty AgentHeadless")
	}
}
