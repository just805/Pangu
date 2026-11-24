package db

import "testing"

func TestAggregateBenchmark(t *testing.T) {
	rows := []BenchmarkTask{
		{TaskID: 1, Success: true, Findings: 6, FalsePositives: 1, UnverifiedHigh: 1, Tokens: 1000, DurationMinutes: 30},
		{TaskID: 2, Success: false, Findings: 4, FalsePositives: 1, UnverifiedHigh: 0, Tokens: 3000, DurationMinutes: 60},
	}
	got := aggregateBenchmark(rows)
	if got.TaskCount != 2 || got.SuccessCount != 1 {
		t.Fatalf("task/success count = %d/%d, want 2/1", got.TaskCount, got.SuccessCount)
	}
	if got.SuccessRate != 0.5 {
		t.Errorf("success rate = %v, want 0.5", got.SuccessRate)
	}
	if got.Findings != 10 || got.FalsePositives != 2 || got.UnverifiedHigh != 1 {
		t.Errorf("findings/fp/unverified = %d/%d/%d, want 10/2/1", got.Findings, got.FalsePositives, got.UnverifiedHigh)
	}
	if got.FalsePositiveRate != 0.2 {
		t.Errorf("fp rate = %v, want 0.2", got.FalsePositiveRate)
	}
	if got.UnverifiedRate != 0.1 {
		t.Errorf("unverified rate = %v, want 0.1", got.UnverifiedRate)
	}
	if got.TotalTokens != 4000 || got.AvgTokens != 2000 {
		t.Errorf("tokens = %d avg=%v, want 4000/2000", got.TotalTokens, got.AvgTokens)
	}
	if got.AvgDurationMin != 45 {
		t.Errorf("avg duration = %v, want 45", got.AvgDurationMin)
	}
}

func TestAggregateBenchmarkEmpty(t *testing.T) {
	got := aggregateBenchmark(nil)
	if got.TaskCount != 0 || got.SuccessRate != 0 || got.FalsePositiveRate != 0 || got.UnverifiedRate != 0 {
		t.Fatalf("empty benchmark must be all-zero without NaN: %+v", got)
	}
}
