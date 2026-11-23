package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 合并文件落盘必须处理同名冲突（a.exe / a-1.exe），且不残留源文件。
func TestSaveMergedUnique(t *testing.T) {
	dir := t.TempDir()
	for i, want := range []string{"a.exe", "a-1.exe"} {
		src := filepath.Join(dir, fmt.Sprintf("src-%d", i))
		if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
		dest, err := saveMergedUnique(dir, "a.exe", src)
		if err != nil {
			t.Fatalf("saveMergedUnique: %v", err)
		}
		if filepath.Base(dest) != want {
			t.Fatalf("dest=%q want %q", filepath.Base(dest), want)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Fatalf("source %q should be removed, err=%v", src, err)
		}
	}
}
