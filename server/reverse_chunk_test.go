package server

import "testing"

func TestReverseChunkHelpers(t *testing.T) {
	if got := reverseChunkCount(0); got != 0 {
		t.Fatalf("chunk count(0)=%d", got)
	}
	if got := reverseChunkCount(1); got != 1 {
		t.Fatalf("chunk count(1)=%d", got)
	}
	if got := reverseChunkCount(reverseChunkSize); got != 1 {
		t.Fatalf("chunk count(chunk)=%d", got)
	}
	if got := reverseChunkCount(reverseChunkSize + 1); got != 2 {
		t.Fatalf("chunk count(chunk+1)=%d", got)
	}
	if got := reverseChunkCount(maxReverseSampleBytes); got > reverseChunkMaxIndex {
		t.Fatalf("512MB 需要 %d 片，超过上限 %d", got, reverseChunkMaxIndex)
	}
	if !validReverseUploadID("0123456789abcdef0123456789abcdef") {
		t.Fatal("合法 32 位 hex 上传 ID 被拒绝")
	}
	for _, bad := range []string{"", "../etc/passwd", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdeg"} {
		if validReverseUploadID(bad) {
			t.Fatalf("非法上传 ID %q 被接受", bad)
		}
	}
}
