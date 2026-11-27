package db

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 这组测试守的是「schema.sql 是单一数据源」这条不变量。
//
// 背景（2026-11-27）：llm_usage / llm_records / intent_reflections / task_artifacts
// 四张表的 DDL 原先只在代码里（server 建 Manager 时 CREATE TABLE IF NOT EXISTS），
// 后果是 `psql < schema.sql` 手工初始化会缺表、备份/迁移/文档失真、启动表数在
// 「schema 阶段」与「运行稳定态」之间差 4。搬进 schema.sql 时又踩了一次顺序坑：
// CREATE TABLE IF NOT EXISTS 不会给已存在的表补列，若索引建在老库还没有的列上，
// 整个 schema 执行以 42703 失败、启动挂掉。

func readSchema(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("读取 schema.sql: %v", err)
	}
	return string(b)
}

// TestSchemaCoversRuntimeTables 断言 schema.sql 覆盖那四张表与关键补列。
// 未来若有人把某张表又挪回代码里（或新增了代码建表），这条会提醒。
func TestSchemaCoversRuntimeTables(t *testing.T) {
	sql := readSchema(t)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS llm_usage",
		"CREATE TABLE IF NOT EXISTS llm_records",
		"CREATE TABLE IF NOT EXISTS intent_reflections",
		"CREATE TABLE IF NOT EXISTS task_artifacts",
		"ALTER TABLE task_artifacts ADD COLUMN IF NOT EXISTS finding_id",
		"ALTER TABLE llm_usage ADD COLUMN IF NOT EXISTS intent_id",
		"ALTER TABLE llm_records ADD COLUMN IF NOT EXISTS raw_request",
		"ALTER TABLE llm_records ADD COLUMN IF NOT EXISTS raw_response",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("schema.sql 缺少 %q —— 该表/列是否又只存在于代码里？", want)
		}
	}
}

var (
	createIndexRe = regexp.MustCompile(`CREATE INDEX IF NOT EXISTS \w+ ON (\w+)\(([^)]*)\)`)
	alterColumnRe = regexp.MustCompile(`ALTER TABLE (\w+) ADD COLUMN IF NOT EXISTS (\w+)`)
)

// TestSchemaAltersBeforeDependentIndexes 锁住顺序铁律：建表 → 补列 → 建索引。
//
// 判据：schema.sql 里任何 CREATE INDEX ... ON t(col)，如果同一文件里也有
// ALTER TABLE t ADD COLUMN IF NOT EXISTS col，那么 ALTER 的偏移必须更小。
// 顺序反了在老库上表现为启动失败（42703），新库完全看不出来——所以只能靠静态检查。
func TestSchemaAltersBeforeDependentIndexes(t *testing.T) {
	sql := readSchema(t)

	// table.column → 最靠前的补列位置
	alterPos := map[string]int{}
	for _, m := range alterColumnRe.FindAllStringSubmatchIndex(sql, -1) {
		key := sql[m[2]:m[3]] + "." + sql[m[4]:m[5]]
		if _, ok := alterPos[key]; !ok {
			alterPos[key] = m[0]
		}
	}
	if len(alterPos) == 0 {
		t.Fatal("没有扫到任何 ALTER ADD COLUMN —— 正则可能已失效")
	}

	checked := 0
	for _, m := range createIndexRe.FindAllStringSubmatchIndex(sql, -1) {
		table := sql[m[2]:m[3]]
		indexPos := m[0]
		for _, rawCol := range strings.Split(sql[m[4]:m[5]], ",") {
			col := strings.TrimSpace(rawCol)
			// 只比较简单列名（跳过表达式索引 / 排序方向）。
			if col == "" || strings.ContainsAny(col, " ()") {
				continue
			}
			ap, ok := alterPos[table+"."+col]
			if !ok {
				continue
			}
			checked++
			if ap > indexPos {
				t.Errorf("顺序错误：%s(%s) 的索引出现在补列之前。老库缺该列时整个 schema 会 42703 失败——"+
					"把 ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s 移到建索引之前。", table, col, table, col)
			}
		}
	}
	if checked == 0 {
		t.Error("没有任何「索引依赖补列」的组合被检查到——这条测试已失去意义，请复核正则")
	}
}
