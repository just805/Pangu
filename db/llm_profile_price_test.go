package db

import "testing"

// M-15：通道自定义价格的存取。价格是独立列（不进 Insert/Update 大 SQL），
// 这里验证它确实写进去了，且读回时与其它字段互不干扰。
func TestSaveProfilePricesRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	name := "price-roundtrip"
	_, _ = d.Exec(`DELETE FROM llm_profiles WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM llm_profiles WHERE name=$1`, name)

	id, err := d.SaveProfile(&LLMProfile{
		Name: name, Format: "openai", Model: "gpt-x",
		InputPricePerMTok: 2.5, OutputPricePerMTok: 10,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := d.ProfileByID(id)
	if err != nil || got == nil {
		t.Fatalf("profile by id: %v", err)
	}
	if got.InputPricePerMTok != 2.5 || got.OutputPricePerMTok != 10 {
		t.Fatalf("价格读回不符: %+v", got)
	}

	// 更新为 0（清除自定义价，回退内置参考价）。
	if _, err := d.SaveProfile(&LLMProfile{
		ID: id, Name: name, Format: "openai", Model: "gpt-x",
		InputPricePerMTok: 0, OutputPricePerMTok: 0,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = d.ProfileByID(id)
	if err != nil || got == nil {
		t.Fatalf("profile by id 2: %v", err)
	}
	if got.InputPricePerMTok != 0 || got.OutputPricePerMTok != 0 {
		t.Fatalf("清零后应为 0，实际 %+v", got)
	}
	// 负值应被夹到 0。
	if _, err := d.SaveProfile(&LLMProfile{
		ID: id, Name: name, Format: "openai", Model: "gpt-x",
		InputPricePerMTok: -1, OutputPricePerMTok: -5,
	}); err != nil {
		t.Fatalf("update neg: %v", err)
	}
	got, _ = d.ProfileByID(id)
	if got.InputPricePerMTok < 0 || got.OutputPricePerMTok < 0 {
		t.Fatalf("负价应夹到 0，实际 %+v", got)
	}
}

// ListProfiles（UI 用的 hint 变体）也要带出价格，否则编辑表单回显不到。
func TestListProfilesIncludesPrices(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	name := "price-list"
	_, _ = d.Exec(`DELETE FROM llm_profiles WHERE name=$1`, name)
	defer d.Exec(`DELETE FROM llm_profiles WHERE name=$1`, name)
	if _, err := d.SaveProfile(&LLMProfile{
		Name: name, Format: "openai", Model: "m",
		InputPricePerMTok: 1.25, OutputPricePerMTok: 3,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	profiles, err := d.ListProfiles()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, p := range profiles {
		if p.Name == name {
			found = true
			if p.InputPricePerMTok != 1.25 || p.OutputPricePerMTok != 3 {
				t.Fatalf("list 价格不符: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("list 未返回刚创建的通道")
	}
}
