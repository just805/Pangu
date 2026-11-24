package server

import "testing"

func TestEvaluateTaskBudget(t *testing.T) {
	cfg := taskBudgetConfig{Enabled: true, Tokens: 10_000_000, WarnPct: 80}
	cases := []struct {
		name     string
		total    int64
		cfg      taskBudgetConfig
		wantWarn bool
		wantHard bool
	}{
		{"disabled", 20_000_000, taskBudgetConfig{Enabled: false, Tokens: 10_000_000, WarnPct: 80}, false, false},
		{"zero-tokens", 20_000_000, taskBudgetConfig{Enabled: true, Tokens: 0, WarnPct: 80}, false, false},
		{"below-warn", 7_999_999, cfg, false, false},
		{"at-warn", 8_000_000, cfg, true, false},
		{"between", 9_999_999, cfg, true, false},
		{"at-hard", 10_000_000, cfg, true, true},
		{"above-hard", 12_000_000, cfg, true, true},
	}
	for _, tc := range cases {
		warn, hard := evaluateTaskBudget(tc.total, tc.cfg)
		if warn != tc.wantWarn || hard != tc.wantHard {
			t.Errorf("%s: evaluateTaskBudget(%d) = (warn=%v, hard=%v), want (%v, %v)",
				tc.name, tc.total, warn, hard, tc.wantWarn, tc.wantHard)
		}
	}
}

func TestTaskBudgetWarnThresholdRespectsPct(t *testing.T) {
	// 50% 阈值：一半就提醒，但未到硬上限。
	cfg := taskBudgetConfig{Enabled: true, Tokens: 1_000_000, WarnPct: 50}
	if warn, hard := evaluateTaskBudget(500_000, cfg); !warn || hard {
		t.Fatalf("50%% threshold: got warn=%v hard=%v, want true/false", warn, hard)
	}
}
