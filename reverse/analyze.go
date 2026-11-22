package reverse

// 桌面客户端/二进制静态 triage（P0）：不执行样本，只做确定性解析。
// 当前覆盖 Windows PE（头/节区/导入导出/签名/.NET/加壳特征）、ELF、ZIP 类
// （APK/JAR）与 Electron asar 识别；原生反编译在后续批次接入。

import (
	"archive/zip"
	"bytes"
	"debug/elf"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type SectionInfo struct {
	Name       string  `json:"name"`
	Size       uint32  `json:"size"`
	Entropy    float64 `json:"entropy"`
	Executable bool    `json:"executable,omitempty"`
	Writable   bool    `json:"writable,omitempty"`
}

type ImportGroup struct {
	DLL   string   `json:"dll"`
	Funcs []string `json:"funcs"`
}

type Meta struct {
	Format      string        `json:"format"`
	Machine     string        `json:"machine,omitempty"`
	Magic       string        `json:"magic,omitempty"`
	Subsystem   string        `json:"subsystem,omitempty"`
	Timestamp   string        `json:"timestamp,omitempty"`
	EntryPoint  uint64        `json:"entry_point,omitempty"`
	ImageBase   uint64        `json:"image_base,omitempty"`
	DotNet      bool          `json:"dotnet,omitempty"`
	Signed      bool          `json:"signed,omitempty"`
	Packer      string        `json:"packer,omitempty"`
	PDB         string        `json:"pdb,omitempty"`
	Mitigations []string      `json:"mitigations,omitempty"`
	Sections    []SectionInfo `json:"sections,omitempty"`
	Imports     []ImportGroup `json:"imports,omitempty"`
	Exports     []string      `json:"exports,omitempty"`
	URLs        []string      `json:"urls,omitempty"`
	Domains     []string      `json:"domains,omitempty"`
	StringsTop  []string      `json:"strings_top,omitempty"`
	StringCount int           `json:"string_count"`
	SecretCount int           `json:"secret_count"`
	Note        string        `json:"note,omitempty"`
}

// Result 是一次静态分析的结果。
type Result struct {
	Kind    string   `json:"kind"`
	Meta    Meta     `json:"metadata"`
	Secrets []Secret `json:"secrets"`
}

// Analyze 对上传样本做静态 triage + 敏感信息扫描。绝不执行样本。
func Analyze(path, originalName string) (*Result, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxAnalyzeBytes {
		return nil, fmt.Errorf("样本超过静态分析上限（%d MB）", maxAnalyzeBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	secrets, interesting, urls, domains, stringCount := extractSecrets(data)
	res := &Result{Kind: "binary", Secrets: secrets}
	res.Meta.Format = "binary"
	res.Meta.URLs = urls
	res.Meta.Domains = domains
	res.Meta.StringsTop = interesting
	res.Meta.StringCount = stringCount
	res.Meta.SecretCount = len(secrets)

	switch {
	case isMZ(data):
		if f, err := pe.Open(path); err == nil {
			defer f.Close()
			res.Kind = "pe"
			fillPE(f, data, &res.Meta)
			for i := range res.Secrets {
				res.Secrets[i].Section = peSectionForOffset(f, res.Secrets[i].Offset)
			}
		} else {
			res.Kind = "pe-invalid"
			res.Meta.Format = "PE (解析失败)"
			res.Meta.Note = "MZ 头存在但 PE 解析失败：" + err.Error()
		}
	case isELF(data):
		res.Kind = "elf"
		fillELF(path, &res.Meta)
	case isZIP(data):
		res.Kind = fillZIP(data, &res.Meta)
	case isASAR(originalName, data):
		res.Kind = "electron-asar"
		res.Meta.Format = "Electron ASAR"
		res.Meta.Note = "检测到 Electron asar 包；解包与 JS 审计在后续批次接入。"
	default:
		res.Kind = "binary"
		res.Meta.Format = "unknown"
		res.Meta.Note = "未识别为 PE/ELF/ZIP/ASAR，仅做字符串与秘密扫描。"
	}
	return res, nil
}

const maxAnalyzeBytes = 256 << 20 // 256MB

func fillPE(f *pe.File, data []byte, m *Meta) {
	m.Format = "PE"
	m.Machine = peMachineName(f.Machine)
	m.Timestamp = time.Unix(int64(f.TimeDateStamp), 0).UTC().Format(time.RFC3339)

	var dllChars uint16
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		m.Magic = "PE32"
		m.Subsystem = peSubsystemName(oh.Subsystem)
		m.EntryPoint = uint64(oh.AddressOfEntryPoint)
		m.ImageBase = uint64(oh.ImageBase)
		dllChars = oh.DllCharacteristics
		m.DotNet = oh.DataDirectory[14].VirtualAddress != 0
		m.Signed = oh.DataDirectory[4].VirtualAddress != 0
	case *pe.OptionalHeader64:
		m.Magic = "PE32+"
		m.Subsystem = peSubsystemName(oh.Subsystem)
		m.EntryPoint = uint64(oh.AddressOfEntryPoint)
		m.ImageBase = oh.ImageBase
		dllChars = oh.DllCharacteristics
		m.DotNet = oh.DataDirectory[14].VirtualAddress != 0
		m.Signed = oh.DataDirectory[4].VirtualAddress != 0
	}

	if dllChars&0x0040 != 0 {
		m.Mitigations = append(m.Mitigations, "ASLR")
	}
	if dllChars&0x0100 != 0 {
		m.Mitigations = append(m.Mitigations, "DEP")
	}
	if dllChars&0x4000 != 0 {
		m.Mitigations = append(m.Mitigations, "CFG")
	}
	if dllChars&0x0020 != 0 {
		m.Mitigations = append(m.Mitigations, "HighEntropyVA")
	}

	for _, s := range f.Sections {
		name := strings.TrimRight(s.Name, "\x00")
		si := SectionInfo{
			Name:       name,
			Size:       s.Size,
			Executable: s.Characteristics&0x20000000 != 0,
			Writable:   s.Characteristics&0x80000000 != 0,
		}
		n := int64(s.Size)
		if n > 8<<20 {
			n = 8 << 20
		}
		if n > 0 {
			buf := make([]byte, n)
			if _, err := s.ReadAt(buf, 0); err == nil {
				si.Entropy = shannonEntropy(buf)
			}
		}
		m.Sections = append(m.Sections, si)
		if p := packerFromSection(name); p != "" && m.Packer == "" {
			m.Packer = p
		}
		if si.Executable && si.Entropy >= 7.2 && m.Packer == "" {
			m.Packer = "疑似加壳（高熵可执行节区）"
		}
	}

	syms, _ := f.ImportedSymbols()
	byDLL := map[string][]string{}
	for _, sym := range syms {
		dll, fn, ok := strings.Cut(sym, ":")
		if !ok || dll == "" || fn == "" {
			continue
		}
		if len(byDLL[dll]) < 12 {
			byDLL[dll] = append(byDLL[dll], fn)
		}
	}
	dlls := make([]string, 0, len(byDLL))
	for dll := range byDLL {
		dlls = append(dlls, dll)
	}
	sort.Strings(dlls)
	for i, dll := range dlls {
		if i >= 30 {
			break
		}
		m.Imports = append(m.Imports, ImportGroup{DLL: dll, Funcs: byDLL[dll]})
	}
	// debug/pe 不暴露导出表；P0 先保留导入表/节区/签名等可用信息。

	head := data
	if len(head) > 16<<20 {
		head = head[:16<<20]
	}
	if p := pdbPathRe.Find(head); len(p) > 0 {
		m.PDB = truncate(string(p), 260)
	}
	if m.DotNet {
		m.Note = "检测到 .NET/CLR；可接入 ILSpy 反编译（后续批次）。"
	}
}

func fillELF(path string, m *Meta) {
	m.Format = "ELF"
	m.Machine = "unknown"
	f, err := elf.Open(path)
	if err != nil {
		m.Note = "ELF 解析失败：" + err.Error()
		return
	}
	defer f.Close()
	m.Machine = f.Machine.String()
	m.Magic = f.Class.String()
	for _, s := range f.Sections {
		m.Sections = append(m.Sections, SectionInfo{Name: s.Name, Size: uint32(s.Size)})
	}
	if len(m.Sections) > 40 {
		m.Sections = m.Sections[:40]
	}
}

func fillZIP(data []byte, m *Meta) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		m.Format = "ZIP (解析失败)"
		m.Note = err.Error()
		return "zip"
	}
	kind := "zip"
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name == "AndroidManifest.xml" {
			kind = "apk"
		}
		if f.Name == "META-INF/MANIFEST.MF" && kind == "zip" {
			kind = "jar"
		}
		if len(names) >= 300 {
			break
		}
	}
	switch kind {
	case "apk":
		m.Format = "Android APK"
		m.Note = "检测到 APK；jadx/apktool 反编译在现有 apk-reverse 技能中接入。"
	case "jar":
		m.Format = "Java JAR"
		m.Note = "检测到 JAR；可接入 Java 反编译（后续批次）。"
	default:
		m.Format = "ZIP"
	}
	m.StringsTop = append(m.StringsTop, names...)
	return kind
}

func peSectionForOffset(f *pe.File, off int64) string {
	for _, s := range f.Sections {
		start := int64(s.Offset)
		end := start + int64(s.Size)
		if off >= start && off < end {
			return strings.TrimRight(s.Name, "\x00")
		}
	}
	return "headers"
}

func peMachineName(m uint16) string {
	switch m {
	case 0x014c:
		return "I386"
	case 0x8664:
		return "AMD64"
	case 0x01c0:
		return "ARM"
	case 0xaa64:
		return "ARM64"
	}
	return fmt.Sprintf("0x%04x", m)
}

func peSubsystemName(s uint16) string {
	switch s {
	case 1:
		return "Native"
	case 2:
		return "Windows GUI"
	case 3:
		return "Windows Console"
	case 9:
		return "Windows CE"
	}
	return fmt.Sprintf("0x%04x", s)
}

func packerFromSection(name string) string {
	l := strings.ToLower(name)
	for _, p := range []struct {
		key  string
		name string
	}{
		{"upx", "UPX"},
		{"aspack", "ASPack"},
		{"adata", "ASPack"},
		{"themida", "Themida"},
		{"vmp", "VMProtect"},
		{"petite", "Petite"},
		{"mpress", "MPRESS"},
		{"enigma", "Enigma"},
	} {
		if strings.Contains(l, p.key) {
			return p.name
		}
	}
	return ""
}

func shannonEntropy(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var counts [256]int
	for _, c := range b {
		counts[c]++
	}
	n := float64(len(b))
	var e float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		e -= p * math.Log2(p)
	}
	return math.Round(e*100) / 100
}

func isMZ(b []byte) bool {
	return len(b) >= 2 && b[0] == 'M' && b[1] == 'Z'
}

func isELF(b []byte) bool {
	return len(b) >= 4 && b[0] == 0x7f && b[1] == 'E' && b[2] == 'L' && b[3] == 'F'
}

func isZIP(b []byte) bool {
	return len(b) >= 4 && b[0] == 'P' && b[1] == 'K' &&
		((b[2] == 3 && b[3] == 4) || (b[2] == 5 && b[3] == 6) || (b[2] == 7 && b[3] == 8))
}

func isASAR(name string, b []byte) bool {
	if strings.EqualFold(filepath.Ext(name), ".asar") {
		return true
	}
	// asar header: 前 16 字节是长度字段，随后是 JSON；"files" 是根键。
	if len(b) > 64 && binary.LittleEndian.Uint32(b[:4]) == 4 {
		head := b
		if len(head) > 4096 {
			head = head[:4096]
		}
		return bytes.Contains(head, []byte(`"files"`))
	}
	return false
}

var pdbPathRe = regexp.MustCompile(`(?i)[a-z]:\\[^\x00\r\n]{1,200}\.pdb`)
