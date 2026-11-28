package traffic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedExchange(t *testing.T, tr *Traffic, id string, ts int64, host string) {
	t.Helper()
	if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,'')`,
		id, ts, host, "GET", "/", "http://"+host+"/", 200, "text/html", 0, 0); err != nil {
		t.Fatal(err)
	}
}

func countExchanges(t *testing.T, tr *Traffic) int {
	t.Helper()
	var n int
	if err := tr.DB().QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPruneByAge：只删早于阈值的行，晚于阈值的原样保留；零值配置是 no-op。
func TestPruneByAge(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	seedExchange(t, tr, "1-0001", 1000, "old.example.com")
	seedExchange(t, tr, "1-0002", 2000, "mid.example.com")
	seedExchange(t, tr, "1-0003", 3000, "new.example.com")

	// 最旧行的正文挂在 blob 上：按年龄删除后也必须 GC，否则磁盘不会降。
	data := bytes.Repeat([]byte("B"), 1<<20)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	blobPath := filepath.Join(dir, "_blobs", "sha256", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DB().Exec(`INSERT INTO blob_refs(hash,exchange_id) VALUES(?,?)`, hash, "1-0001"); err != nil {
		t.Fatal(err)
	}

	if res, err := tr.Prune(PruneOptions{}); err != nil || res.Deleted != 0 {
		t.Fatalf("zero options should be a no-op: deleted=%d err=%v", res.Deleted, err)
	}

	res, err := tr.Prune(PruneOptions{OlderThan: timeAt(2500)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 {
		t.Fatalf("deleted=%d, want 2", res.Deleted)
	}
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Fatalf("orphan blob still exists after age prune (stat err=%v)", err)
	}
	if got := countExchanges(t, tr); got != 1 {
		t.Fatalf("remaining=%d, want 1", got)
	}
	var id string
	if err := tr.DB().QueryRow(`SELECT id FROM exchanges`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "1-0003" {
		t.Fatalf("remaining id=%q, want 1-0003", id)
	}
}

// TestPruneBySize：超过容量上限时从最旧开始删；引用已被删除的 blob 由 GC 回收，
// 且不会误删较新的流量。
func TestPruneBySize(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	const oldID = "1-0001"
	seedExchange(t, tr, oldID, 1000, "old.example.com")
	seedExchange(t, tr, "1-0002", 2000, "mid.example.com")
	seedExchange(t, tr, "1-0003", 3000, "new.example.com")

	// 8MB 正文只挂在最旧的行上，确保「删最旧 + GC」能明显低于容量线。
	data := bytes.Repeat([]byte("A"), 8<<20)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	blobPath := filepath.Join(dir, "_blobs", "sha256", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DB().Exec(`INSERT INTO blob_refs(hash,exchange_id) VALUES(?,?)`, hash, oldID); err != nil {
		t.Fatal(err)
	}

	before := tr.UsageNow()
	if before.Bytes < int64(len(data)) {
		t.Fatalf("usage=%d, want at least blob size %d", before.Bytes, len(data))
	}
	cap := before.Bytes - int64(len(data))/2 // 删掉 8MB 后必然低于这条件

	res, err := tr.Prune(PruneOptions{MaxBytes: cap, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("deleted=%d, want 1 (only the oldest)", res.Deleted)
	}
	if got := countExchanges(t, tr); got != 2 {
		t.Fatalf("remaining=%d, want 2", got)
	}
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Fatalf("orphan blob still exists (stat err=%v)", err)
	}
	if res.Usage.Bytes > cap {
		t.Fatalf("usage=%d still above cap=%d", res.Usage.Bytes, cap)
	}
}

// timeAt 把测试里的秒级时间戳变成 time.Time，保持与 exchanges.ts 的单位一致。
func timeAt(sec int64) time.Time {
	return time.Unix(sec, 0)
}
