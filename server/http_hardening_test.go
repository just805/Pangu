package server

import "testing"

// 大样本上传路径必须命中 30 分钟读限时；普通任务 API 仍保持 5 分钟。
func TestIsLongBodyPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/workspace/upload", true},
		{"/api/chat/upload", true},
		{"/api/tasks/12/reverse/samples", true},
		{"/api/tasks/12/reverse/uploads", false},
		{"/api/tasks/12/reverse/uploads/abc/chunks/0", true},
		{"/api/tasks/12/reverse/samples/3", false},
		{"/api/tasks/12/reverse/decompiled", false},
		{"/api/tasks/12", false},
		{"/api/health", false},
	}
	for _, c := range cases {
		if got := isLongBodyPath(c.path); got != c.want {
			t.Errorf("isLongBodyPath(%q)=%v want %v", c.path, got, c.want)
		}
	}
}
