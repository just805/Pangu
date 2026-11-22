package db

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReverseSampleCRUD(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil || dsn == "" {
		t.Skip("requires PANGU_PG_DSN")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()

	task, err := d.CreateTask("reverse test", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	s := &ReverseSample{
		TaskID:      task.ID,
		Filename:    "client.exe",
		SHA256:      "deadbeef",
		SizeBytes:   1024,
		Status:      "uploaded",
		Metadata:    json.RawMessage(`{"format":"PE"}`),
		StoragePath: "/tmp/deadbeef",
	}
	inserted, err := d.InsertReverseSample(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || s.ID == 0 {
		t.Fatalf("inserted=%v id=%d", inserted, s.ID)
	}
	// 同任务同 sha 去重。
	dup := &ReverseSample{TaskID: task.ID, Filename: "again.exe", SHA256: "deadbeef", SizeBytes: 1024}
	inserted, err = d.InsertReverseSample(ctx, dup)
	if err != nil {
		t.Fatal(err)
	}
	if inserted || dup.ID != s.ID {
		t.Fatalf("duplicate inserted=%v id=%d want existing %d", inserted, dup.ID, s.ID)
	}

	if err := d.UpdateReverseSampleAnalysis(s.ID, "pe", "analyzed", json.RawMessage(`{"format":"PE","dotnet":true}`), ""); err != nil {
		t.Fatal(err)
	}
	secrets := []ReverseSecret{
		{Rule: "generic_secret", Match: "S3cr3tP4ssw0rd", Context: "password = S3cr3tP4ssw0rd", Offset: 42, Section: ".rdata", Confidence: 0.5},
		{Rule: "aws_access_key", Match: "AKIA1234567890ABCDEF", Context: "AKIA...", Offset: 100, Confidence: 0.95},
	}
	if err := d.ReplaceReverseSecrets(ctx, s.ID, "binary", secrets); err != nil {
		t.Fatal(err)
	}
	// 反编译来源的秘密独立保存，不覆盖二进制扫描结果。
	if err := d.ReplaceReverseSecrets(ctx, s.ID, "decompile", []ReverseSecret{
		{Rule: "generic_secret", Match: "DecompiledSecret1", Context: "var token = ...", Section: "Program.cs", Confidence: 0.5},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := d.ListReverseSecrets(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("secrets=%+v", got)
	}
	sources := map[string]int{}
	for _, x := range got {
		sources[x.Source]++
	}
	if sources["binary"] != 2 || sources["decompile"] != 1 {
		t.Fatalf("sources=%v secrets=%+v", sources, got)
	}
	if err := d.UpdateReverseSampleDecompile(s.ID, "ilspy", "done", "/tmp/decompiled", ""); err != nil {
		t.Fatal(err)
	}
	sample, err := d.GetReverseSample(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sample.Kind != "pe" || sample.Status != "analyzed" || sample.SecretCount != 3 {
		t.Fatalf("sample=%+v", sample)
	}
	if sample.DecompileEngine != "ilspy" || sample.DecompileStatus != "done" || sample.DecompiledAt == nil {
		t.Fatalf("decompile fields=%+v", sample)
	}
	if sample.AnalyzedAt == nil {
		t.Fatal("analyzed_at should be set")
	}
	list, err := d.ListReverseSamples(task.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}

	path, err := d.DeleteReverseSample(task.ID, s.ID)
	if err != nil || path != "/tmp/deadbeef" {
		t.Fatalf("delete path=%q err=%v", path, err)
	}
	if _, err := d.GetReverseSample(s.ID); err == nil {
		t.Fatal("sample should be gone")
	}
}
