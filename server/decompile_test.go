package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/just805/pangu/db"
)

func TestSafeDecompilePath(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "..", "."} {
		if _, ok := safeDecompilePath(root, bad); ok {
			t.Fatalf("path %q should be rejected", bad)
		}
	}
	got, ok := safeDecompilePath(root, "src/Program.cs")
	if !ok || !strings.HasPrefix(got, root) {
		t.Fatalf("valid path rejected: %q ok=%v", got, ok)
	}
}

func TestDecompileEngineFor(t *testing.T) {
	srv := &Server{}
	cases := []struct {
		name string
		s    *db.ReverseSample
		want string
	}{
		{"dotnet", &db.ReverseSample{Kind: "pe", Metadata: json.RawMessage(`{"dotnet":true}`)}, "ilspy"},
		{"asar", &db.ReverseSample{Kind: "electron-asar", Metadata: json.RawMessage(`{}`)}, "asar"},
		{"apk", &db.ReverseSample{Kind: "apk", Metadata: json.RawMessage(`{}`)}, "jadx"},
		{"jar", &db.ReverseSample{Kind: "jar", Metadata: json.RawMessage(`{}`)}, "jadx"},
		{"native-pe", &db.ReverseSample{Kind: "pe", Metadata: json.RawMessage(`{}`)}, "ghidra"},
	}
	for _, c := range cases {
		got, err := srv.decompileEngineFor(c.s, "auto")
		if err != nil || got != c.want {
			t.Fatalf("%s: engine=%q err=%v want %q", c.name, got, err, c.want)
		}
	}
	if _, err := srv.decompileEngineFor(&db.ReverseSample{Kind: "elf"}, "auto"); err == nil {
		t.Fatal("elf auto-decompile should return an explanatory error")
	}
	if got, err := srv.decompileEngineFor(&db.ReverseSample{Kind: "pe"}, "asar"); err != nil || got != "asar" {
		t.Fatalf("explicit engine not honored: %q %v", got, err)
	}
}

func TestListDecompileFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "Program.cs"), []byte("class Program {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	files, total, err := listDecompileFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || len(files) != 2 {
		t.Fatalf("files=%+v total=%d", files, total)
	}
	if files[0].Path != "app.bin" || files[1].Path != "src/Program.cs" {
		t.Fatalf("not sorted/relative: %+v", files)
	}
	if !files[1].Text || files[0].Text {
		t.Fatalf("text detection wrong: %+v", files)
	}
}

func TestGhidraScriptEmbedded(t *testing.T) {
	if !strings.Contains(ghidraDecompileScript, "class DecompileAll extends GhidraScript") {
		t.Fatal("embedded Ghidra script is missing the DecompileAll class")
	}
	for _, want := range []string{"getScriptArgs", "DecompInterface", "getFunctions"} {
		if !strings.Contains(ghidraDecompileScript, want) {
			t.Fatalf("embedded Ghidra script is missing %q", want)
		}
	}
}

func TestIsTextSourceGhidraOutput(t *testing.T) {
	for _, name := range []string{"00401000_main.c", "types.h", "impl.cpp", "impl.hpp", "unit.cc", "defs.inc"} {
		if !isTextSource(name) {
			t.Fatalf("%s should be treated as text", name)
		}
	}
	if isTextSource("app.bin") {
		t.Fatal("binary file should not be treated as text")
	}
}
