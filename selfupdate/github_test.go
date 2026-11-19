package selfupdate

import (
	"strings"
	"testing"
)

// 回归：Repo 必须是 owner/name——latestURL/tagURL 自己拼 API 路径，Repo 带
// github.com/ 前缀会拼出 /repos/github.com/owner/name 恒 404（改名批次踩过，
// 前端报「仓库没有最新正式版」，实际是路径拼错）。
func TestRepoIsOwnerSlashName(t *testing.T) {
	if strings.Contains(Repo, "/") {
		if parts := strings.Split(Repo, "/"); len(parts) != 2 || parts[0] == "github.com" {
			t.Fatalf("Repo=%q 必须是 owner/name 形态（不带 github.com/ 前缀）", Repo)
		}
	}
	for _, u := range []string{latestURL(), tagURL("v0.1.3")} {
		if strings.Contains(u, "/repos/github.com/") {
			t.Fatalf("API 路径拼进了多余前缀: %s", u)
		}
	}
}

// TestUpdateURLShape：API 路径与资产名的形状（与 build.sh 的打包名一致）。
func TestUpdateURLShape(t *testing.T) {
	const want = "https://api.github.com/repos/just805/pangu/releases/latest"
	if got := latestURL(); got != want {
		t.Fatalf("latestURL = %q, want %q", got, want)
	}
	if got := tagURL("v0.1.3"); got != "https://api.github.com/repos/just805/pangu/releases/tags/v0.1.3" {
		t.Fatalf("tagURL = %q", got)
	}
	if got := AssetName("v0.1.3", "linux", "amd64"); got != "pangu-0.1.3-linux-amd64.zip" {
		t.Fatalf("AssetName = %q", got)
	}
}
