"use client";

import * as React from "react";

import { Code2, Cpu, Download, FileSearch, RefreshCw, RotateCw, ShieldAlert, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { ReverseDecompiled, ReverseSample, ReverseSampleDetail } from "@/lib/types";
import { cn } from "@/lib/utils";

const MAX_SAMPLE_BYTES = 512 * 1024 * 1024;
// 超过该阈值改走分片上传：单片 16MB，低于 Cloudflare 各档位单请求体限制。
const CHUNK_UPLOAD_THRESHOLD = 80 * 1024 * 1024;

// 代理层（Cloudflare/Nginx）的 413 响应体是 HTML，不直接展示给用户。
function uploadErrorMessage(e: unknown): string {
  const err = e as Error & { status?: number };
  if (err.status === 413) {
    return "上传被 Cloudflare/反向代理拒绝（413 Payload Too Large），分片上传也未通过，请检查代理的请求体限制。";
  }
  if (typeof err.status === "number") {
    return `上传失败（HTTP ${err.status}）`;
  }
  return err.message || "上传失败";
}

const kindCN: Record<string, string> = {
  pe: "Windows PE",
  "pe-invalid": "PE（解析失败）",
  elf: "ELF",
  apk: "Android APK",
  jar: "Java JAR",
  zip: "ZIP",
  "electron-asar": "Electron ASAR",
  binary: "二进制",
};

const statusCN: Record<string, string> = {
  uploaded: "待分析",
  analyzing: "分析中",
  analyzed: "已分析",
  failed: "分析失败",
};

const decompileStatusCN: Record<string, string> = {
  running: "反编译中",
  done: "反编译完成",
  failed: "反编译失败",
};

const ruleCN: Record<string, string> = {
  private_key: "私钥",
  aws_access_key: "AWS Access Key",
  aws_secret_key: "AWS Secret Key",
  github_token: "GitHub Token",
  google_api_key: "Google API Key",
  slack_token: "Slack Token",
  stripe_key: "Stripe Key",
  aliyun_access_key: "阿里云 AccessKey",
  tencent_secret_id: "腾讯云 SecretId",
  jwt: "JWT",
  bearer_token: "Bearer Token",
  basic_auth: "Basic Auth",
  database_url: "数据库连接串",
  jdbc_url: "JDBC 连接串",
  internal_url: "内网地址",
  generic_secret: "通用凭据",
};

function fmtSize(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(2)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}

function maskValue(s: string): string {
  if (s.length <= 8) return "****";
  return `${s.slice(0, 4)}…${s.slice(-4)}`;
}

export function ReverseTab({ taskId }: { taskId: string }) {
  const [samples, setSamples] = React.useState<ReverseSample[]>([]);
  const [detail, setDetail] = React.useState<ReverseSampleDetail | null>(null);
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [uploading, setUploading] = React.useState(false);
  const [uploadProgress, setUploadProgress] = React.useState<number | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const [showPlain, setShowPlain] = React.useState(false);
  const [decompiled, setDecompiled] = React.useState<ReverseDecompiled | null>(null);
  const [decompiling, setDecompiling] = React.useState(false);
  const [selectedFile, setSelectedFile] = React.useState<string | null>(null);
  const [fileContent, setFileContent] = React.useState("");
  const [fileLoading, setFileLoading] = React.useState(false);
  const fileRef = React.useRef<HTMLInputElement>(null);
  const polling = React.useRef<Set<number>>(new Set());

  const openFile = React.useCallback(
    async (sampleId: number, path: string) => {
      setSelectedFile(path);
      setFileLoading(true);
      try {
        setFileContent(await api.reverseDecompiledFile(taskId, sampleId, path));
      } catch (e: unknown) {
        setFileContent("");
        toast.error(e instanceof Error ? e.message : "读取源码失败");
      } finally {
        setFileLoading(false);
      }
    },
    [taskId],
  );

  const loadDecompiled = React.useCallback(
    async (sampleId: number) => {
      setDecompiled(null);
      setSelectedFile(null);
      setFileContent("");
      try {
        const dec = await api.reverseDecompiled(taskId, sampleId);
        setDecompiled(dec);
        const first = dec.files?.find((f) => f.text);
        if (first) void openFile(sampleId, first.path);
      } catch {
        /* 未反编译或接口不可用时保持空 */
      }
    },
    [taskId, openFile],
  );

  // 分片上传的静态分析在服务端后台执行：轮询到 analyzing 结束再刷新详情。
  const pollSample = React.useCallback(
    async (sampleId: number) => {
      if (polling.current.has(sampleId)) return;
      polling.current.add(sampleId);
      try {
        for (let i = 0; i < 240; i++) {
          await new Promise((resolve) => window.setTimeout(resolve, 2500));
          const d = await api.reverseSample(taskId, sampleId);
          if (d.sample.status === "analyzing") continue;
          setDetail((prev) => (prev?.sample.id === sampleId ? d : prev));
          setSamples(await api.reverseSamples(taskId));
          if (d.sample.status === "analyzed") {
            toast.success(`分析完成：${d.secrets.length} 条敏感信息候选`);
          } else {
            toast.error(d.sample.error || "样本分析失败");
          }
          void loadDecompiled(sampleId);
          return;
        }
      } catch {
        /* 轮询失败保持现状，用户可手动刷新 */
      } finally {
        polling.current.delete(sampleId);
      }
    },
    [taskId, loadDecompiled],
  );

  const load = React.useCallback(
    async (select?: number | null) => {
      setLoading(true);
      setError("");
      try {
        const list = await api.reverseSamples(taskId);
        setSamples(list);
        const id = select ?? selectedId ?? list[0]?.id ?? null;
        setSelectedId(id);
        if (id) {
          const d = await api.reverseSample(taskId, id);
          setDetail(d);
          if (d.sample.status === "analyzing") void pollSample(id);
          void loadDecompiled(id);
        } else {
          setDetail(null);
        }
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : "加载逆向样本失败");
      } finally {
        setLoading(false);
      }
    },
    [taskId, selectedId, loadDecompiled, pollSample],
  );

  React.useEffect(() => {
    setSamples([]);
    setDetail(null);
    setSelectedId(null);
    setDecompiled(null);
    setSelectedFile(null);
    setFileContent("");
    void load(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  // 原生 PE 的 Ghidra 反编译是异步的：状态为 running 时定时轮询，直到完成/失败。
  React.useEffect(() => {
    if (decompiled?.status !== "running" || !selectedId) return;
    let cancelled = false;
    const timer = window.setTimeout(async () => {
      try {
        const dec = await api.reverseDecompiled(taskId, selectedId);
        if (cancelled) return;
        setDecompiled(dec);
        if (dec.status !== "running") {
          setDetail(await api.reverseSample(taskId, selectedId));
          setSamples(await api.reverseSamples(taskId));
          if (dec.status === "done") {
            toast.success(`反编译完成：${dec.files.length} 个文件`);
            const first = dec.files.find((f) => f.text);
            if (first) void openFile(selectedId, first.path);
          } else if (dec.status === "failed") {
            toast.error(dec.error || "反编译失败");
          }
        }
      } catch {
        /* 轮询失败保持现状，用户可手动刷新 */
      }
    }, 3000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [decompiled, selectedId, taskId, openFile]);

  // 上传失败时保留 File 对象供一键重试——大样本重新找文件代价高。
  const [failedFile, setFailedFile] = React.useState<File | null>(null);

  const uploadFile = async (file: File) => {
    if (file.size > MAX_SAMPLE_BYTES) {
      const msg = `样本超过 ${MAX_SAMPLE_BYTES >> 20}MB 上限，无法上传`;
      setError(msg);
      toast.error(msg);
      return;
    }
    setUploading(true);
    setUploadProgress(file.size > CHUNK_UPLOAD_THRESHOLD ? 0 : null);
    setError("");
    try {
      const runChunked = async () => {
        setUploadProgress(0);
        return api.reverseUploadChunked(taskId, file, (sent) =>
          setUploadProgress(Math.min(100, Math.round((sent / file.size) * 100))),
        );
      };
      let d: ReverseSampleDetail;
      if (file.size > CHUNK_UPLOAD_THRESHOLD) {
        d = await runChunked();
      } else {
        try {
          d = await api.reverseUpload(taskId, file);
        } catch (e) {
          // 兜底：Cloudflare/反代可能把阈值压得比我们预期更低，413 时自动改分片。
          if ((e as { status?: number }).status === 413) {
            toast.info("单请求上传被代理拒绝，自动切换分片上传…");
            d = await runChunked();
          } else {
            throw e;
          }
        }
      }
      const analyzing = d.sample.status === "analyzing";
      if (analyzing) {
        toast.info(`已上传 ${d.sample.filename}，正在后台分析…`);
      } else {
        toast.success(`已分析 ${d.sample.filename}，命中 ${d.secrets.length} 条敏感信息候选`);
      }
      setDetail(d);
      setSelectedId(d.sample.id);
      const list = await api.reverseSamples(taskId);
      setSamples(list);
      if (analyzing) {
        void pollSample(d.sample.id);
      } else {
        void loadDecompiled(d.sample.id);
      }
    } catch (err: unknown) {
      const msg = uploadErrorMessage(err);
      setError(msg + `（文件 ${file.name} 已保留，可点重试）`);
      setFailedFile(file);
      toast.error(msg);
    } finally {
      setUploading(false);
      setUploadProgress(null);
    }
  };

  const onUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const input = e.currentTarget;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    setFailedFile(null);
    await uploadFile(file);
  };

  const retryUpload = async () => {
    if (!failedFile) return;
    const file = failedFile;
    setFailedFile(null);
    setError("");
    await uploadFile(file);
  };

  const select = async (id: number) => {
    setSelectedId(id);
    setLoading(true);
    setError("");
    try {
      const d = await api.reverseSample(taskId, id);
      setDetail(d);
      if (d.sample.status === "analyzing") void pollSample(id);
      void loadDecompiled(id);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "加载样本失败");
    } finally {
      setLoading(false);
    }
  };

  const reanalyze = async () => {
    if (!selectedId) return;
    setBusy(true);
    try {
      const d = await api.reverseAnalyze(taskId, selectedId);
      setDetail(d);
      const list = await api.reverseSamples(taskId);
      setSamples(list);
      toast.success(`重新分析完成：${d.secrets.length} 条候选`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "重新分析失败");
    } finally {
      setBusy(false);
    }
  };

  const decompile = async () => {
    if (!selectedId) return;
    setDecompiling(true);
    try {
      const res = await api.reverseDecompile(taskId, selectedId);
      if (res.error) {
        toast.error(res.error);
      } else if (res.status === "running" || res.sample?.decompile_status === "running") {
        toast.info("已开始反编译，Ghidra 处理通常需要数分钟，完成后会自动刷新。");
      } else {
        toast.success(`反编译完成：${res.files.length} 个文件，新增 ${res.secret_count ?? 0} 条秘密候选`);
      }
      setDetail(await api.reverseSample(taskId, selectedId));
      await loadDecompiled(selectedId);
      setSamples(await api.reverseSamples(taskId));
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "反编译失败");
    } finally {
      setDecompiling(false);
    }
  };

  const remove = async () => {
    if (!selectedId) return;
    if (!window.confirm("删除该样本及其分析结果？原始文件会一并删除。")) return;
    setBusy(true);
    try {
      await api.reverseDelete(taskId, selectedId);
      toast.success("样本已删除");
      setDetail(null);
      setSelectedId(null);
      setDecompiled(null);
      setSelectedFile(null);
      setFileContent("");
      await load(null);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "删除失败");
    } finally {
      setBusy(false);
    }
  };

  const meta = detail?.sample.metadata;
  const secrets = detail?.secrets ?? [];
  const decompileRunning = detail?.sample.decompile_status === "running" || decompiled?.status === "running";

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-2 font-medium text-sm">
          <Cpu className="size-4 text-muted-foreground" />
          逆向分析
        </div>
        <Badge variant="secondary">样本 {samples.length}</Badge>
        <Badge variant="outline">仅静态分析 · 不执行样本</Badge>
        <div className="ml-auto flex items-center gap-2">
          <input ref={fileRef} type="file" className="hidden" onChange={onUpload} />
          <Button size="sm" onClick={() => fileRef.current?.click()} disabled={uploading}>
            <Upload className={cn("size-3.5", uploading && "animate-pulse")} />
            {uploading
              ? uploadProgress !== null && uploadProgress < 100
                ? `上传 ${uploadProgress}%`
                : "分析中…"
              : "上传样本"}
          </Button>
          <Button size="sm" variant="outline" onClick={() => void load(selectedId)} disabled={loading}>
            <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
            刷新
          </Button>
        </div>
      </div>

      {error ? (
        <div className="flex items-center gap-2">
          <p className="text-destructive text-sm">{error}</p>
          {failedFile && (
            <Button size="sm" variant="outline" disabled={uploading} onClick={() => void retryUpload()}>
              <RefreshCw className={cn("size-3.5", uploading && "animate-spin")} />
              重试上传
            </Button>
          )}
        </div>
      ) : null}

      {samples.length === 0 && !loading ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-2 py-14 text-center">
            <FileSearch className="size-9 text-muted-foreground/60" />
            <p className="font-medium text-sm">还没有上传客户端/二进制样本</p>
            <p className="max-w-lg text-muted-foreground text-xs">
              上传 exe/dll/elf/apk/jar/asar 等样本（单样本 ≤512MB）后，平台会做静态 triage（文件头、节区、熵、导入表、签名、
              .NET/加壳识别）并扫描硬编码凭据。大文件自动分片上传，Cloudflare 下也可用；样本只读，绝不执行。
            </p>
            <Button size="sm" variant="outline" onClick={() => fileRef.current?.click()}>
              <Upload className="size-3.5" />
              选择样本
            </Button>
          </CardContent>
        </Card>
      ) : null}

      {samples.length > 0 ? (
        <div className="grid gap-3 lg:grid-cols-[280px_minmax(0,1fr)]">
          <Card className="h-fit">
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">样本列表</CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              <ScrollArea className="max-h-[70vh]">
                <div className="flex flex-col">
                  {samples.map((s) => (
                    <button
                      key={s.id}
                      type="button"
                      onClick={() => void select(s.id)}
                      className={cn(
                        "border-l-2 px-3 py-2.5 text-left transition-colors hover:bg-muted/60",
                        selectedId === s.id ? "border-primary bg-muted/70" : "border-border",
                      )}
                    >
                      <div className="flex items-center gap-1.5">
                        <span className="min-w-0 flex-1 truncate font-medium text-xs" title={s.filename}>
                          {s.filename}
                        </span>
                        <Badge variant="outline" className="shrink-0 text-[10px]">
                          {kindCN[s.kind] ?? s.kind ?? "未知"}
                        </Badge>
                      </div>
                      <div className="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground">
                        <span>{fmtSize(s.size_bytes)}</span>
                        <span>{statusCN[s.status] ?? s.status}</span>
                        {s.secret_count > 0 ? <span className="text-amber-500">命中 {s.secret_count}</span> : null}
                      </div>
                    </button>
                  ))}
                </div>
              </ScrollArea>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <div className="flex flex-wrap items-center gap-2">
                <CardTitle className="min-w-0 flex-1 truncate text-sm" title={detail?.sample.filename}>
                  {detail?.sample.filename ?? "样本详情"}
                </CardTitle>
                {detail ? (
                  <>
                    <Button size="sm" variant="outline" onClick={decompile} disabled={decompiling || decompileRunning}>
                      <Code2 className={cn("size-3.5", (decompiling || decompileRunning) && "animate-pulse")} />
                      {decompiling || decompileRunning ? "反编译中…" : "反编译"}
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => window.open(api.reverseDownloadUrl(taskId, detail.sample.id), "_blank")}
                    >
                      <Download className="size-3.5" />
                      下载
                    </Button>
                    <Button size="sm" variant="outline" onClick={reanalyze} disabled={busy}>
                      <RotateCw className={cn("size-3.5", busy && "animate-spin")} />
                      重新分析
                    </Button>
                    <Button size="sm" variant="outline" onClick={remove} disabled={busy}>
                      <Trash2 className="size-3.5" />
                      删除
                    </Button>
                  </>
                ) : null}
              </div>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              {!detail ? (
                <p className="py-10 text-center text-muted-foreground text-sm">选择左侧样本查看分析结果。</p>
              ) : (
                <>
                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge variant="secondary">{kindCN[detail.sample.kind] ?? detail.sample.kind}</Badge>
                    <Badge variant="outline">{statusCN[detail.sample.status] ?? detail.sample.status}</Badge>
                    {detail.sample.decompile_status ? (
                      <Badge variant="outline">
                        {decompileStatusCN[detail.sample.decompile_status] ?? detail.sample.decompile_status}
                      </Badge>
                    ) : null}
                    {meta?.dotnet ? <Badge variant="outline">.NET/CLR</Badge> : null}
                    {meta?.signed ? <Badge variant="outline">已签名</Badge> : null}
                    {meta?.packer ? (
                      <Badge variant="outline" className="text-amber-600">
                        壳：{meta.packer}
                      </Badge>
                    ) : null}
                    <span className="font-mono text-[10px] text-muted-foreground" title={detail.sample.sha256}>
                      SHA-256 {detail.sample.sha256.slice(0, 16)}…
                    </span>
                  </div>

                  {detail.sample.error ? <p className="text-destructive text-xs">{detail.sample.error}</p> : null}

                  <div className="grid gap-2 text-xs sm:grid-cols-2 lg:grid-cols-4">
                    <Info label="格式" value={meta?.format} />
                    <Info label="架构" value={meta?.machine} />
                    <Info label="子系统" value={meta?.subsystem} />
                    <Info label="大小" value={fmtSize(detail.sample.size_bytes)} />
                  </div>
                  {meta?.mitigations?.length ? (
                    <p className="text-muted-foreground text-xs">缓解：{meta.mitigations.join(" / ")}</p>
                  ) : null}
                  {meta?.pdb ? (
                    <p className="break-all text-muted-foreground text-xs">PDB：{meta.pdb}</p>
                  ) : null}
                  {meta?.note ? <p className="text-muted-foreground text-xs">{meta.note}</p> : null}

                  {meta?.sections?.length ? (
                    <div>
                      <p className="mb-1.5 font-medium text-xs">节区</p>
                      <div className="overflow-x-auto rounded-md border">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead className="h-8 text-xs">节区</TableHead>
                              <TableHead className="h-8 text-right text-xs">大小</TableHead>
                              <TableHead className="h-8 text-right text-xs">熵</TableHead>
                              <TableHead className="h-8 text-xs">属性</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {meta.sections.map((sec) => (
                              <TableRow key={sec.name}>
                                <TableCell className="py-1.5 font-mono text-xs">{sec.name}</TableCell>
                                <TableCell className="py-1.5 text-right text-xs">{fmtSize(sec.size)}</TableCell>
                                <TableCell className="py-1.5 text-right text-xs">{sec.entropy.toFixed(2)}</TableCell>
                                <TableCell className="py-1.5 text-xs">
                                  {sec.executable ? "X" : ""}
                                  {sec.writable ? "W" : ""}
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      </div>
                    </div>
                  ) : null}

                  <div className="grid gap-3 lg:grid-cols-2">
                    {meta?.imports?.length ? (
                      <div>
                        <p className="mb-1.5 font-medium text-xs">导入表（节选）</p>
                        <ScrollArea className="max-h-52 rounded-md border p-2">
                          <ul className="flex flex-col gap-1 text-xs">
                            {meta.imports.slice(0, 12).map((g) => (
                              <li key={g.dll}>
                                <span className="font-mono">{g.dll}</span>
                                <span className="text-muted-foreground">：{g.funcs.join(", ")}</span>
                              </li>
                            ))}
                          </ul>
                        </ScrollArea>
                      </div>
                    ) : null}
                    {meta?.urls?.length ? (
                      <div>
                        <p className="mb-1.5 font-medium text-xs">字符串中的 URL（节选）</p>
                        <ScrollArea className="max-h-52 rounded-md border p-2">
                          <ul className="flex flex-col gap-1 text-xs">
                            {meta.urls.slice(0, 20).map((u) => (
                              <li key={u} className="break-all font-mono text-muted-foreground">
                                {u}
                              </li>
                            ))}
                          </ul>
                        </ScrollArea>
                      </div>
                    ) : null}
                  </div>

                  {meta?.strings_top?.length ? (
                    <div>
                      <p className="mb-1.5 font-medium text-xs">值得关注的字符串（节选）</p>
                      <ScrollArea className="max-h-40 rounded-md border p-2">
                        <ul className="flex flex-col gap-1 text-xs">
                          {meta.strings_top.slice(0, 15).map((str, i) => (
                            <li key={`${i}-${str.slice(0, 16)}`} className="break-all font-mono text-muted-foreground">
                              {str}
                            </li>
                          ))}
                        </ul>
                      </ScrollArea>
                    </div>
                  ) : null}

                  <div>
                    <div className="mb-1.5 flex items-center gap-2">
                      <p className="font-medium text-xs">敏感信息候选</p>
                      <Badge variant="secondary">{secrets.length}</Badge>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="ml-auto h-6 text-[10px]"
                        onClick={() => setShowPlain((v) => !v)}
                      >
                        {showPlain ? "隐藏明文" : "显示明文"}
                      </Button>
                    </div>
                    {secrets.length === 0 ? (
                      <p className="text-muted-foreground text-xs">
                        未命中规则库；仍建议结合反编译人工复核。规则命中是线索，验证后才能定性为漏洞。
                      </p>
                    ) : (
                      <div className="overflow-x-auto rounded-md border">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead className="h-8 text-xs">规则</TableHead>
                              <TableHead className="h-8 text-xs">命中</TableHead>
                              <TableHead className="h-8 text-xs">位置</TableHead>
                              <TableHead className="h-8 text-right text-xs">置信度</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {secrets.map((s) => (
                              <TableRow key={s.id}>
                                <TableCell className="py-1.5 text-xs">
                                  <span className="inline-flex items-center gap-1">
                                    <ShieldAlert className="size-3 text-amber-500" />
                                    {ruleCN[s.rule] ?? s.rule}
                                  </span>
                                </TableCell>
                                <TableCell className="max-w-[320px] py-1.5 font-mono text-xs">
                                  <span className="break-all">{showPlain ? s.match : maskValue(s.match)}</span>
                                  <div className="mt-0.5 line-clamp-1 text-[10px] text-muted-foreground">{s.context}</div>
                                </TableCell>
                                <TableCell className="py-1.5 font-mono text-xs">
                                  {s.source === "decompile" ? (
                                    <Badge variant="outline" className="mr-1 text-[9px]">
                                      反编译
                                    </Badge>
                                  ) : null}
                                  {s.section ?? "-"} @ 0x{s.offset.toString(16)}
                                </TableCell>
                                <TableCell className="py-1.5 text-right text-xs">
                                  {Math.round(s.confidence * 100)}%
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      </div>
                    )}
                  </div>

                  <div className="border-t pt-3">
                    <div className="mb-1.5 flex flex-wrap items-center gap-2">
                      <p className="font-medium text-xs">反编译产物</p>
                      {detail.sample.decompile_status ? (
                        <Badge variant="outline">
                          {decompileStatusCN[detail.sample.decompile_status] ?? detail.sample.decompile_status}
                        </Badge>
                      ) : null}
                      {decompiled?.engine ? <Badge variant="secondary">{decompiled.engine}</Badge> : null}
                      {decompiled?.files?.length ? (
                        <Badge variant="outline">{decompiled.files.length} 个文件</Badge>
                      ) : null}
                      <span className="ml-auto text-[10px] text-muted-foreground">仅静态反编译 · 不执行样本</span>
                    </div>
                    {detail.sample.decompile_status === "failed" || decompiled?.error ? (
                      <p className="text-destructive text-xs">{decompiled?.error || detail.sample.decompile_error}</p>
                    ) : null}
                    {decompiled?.files?.length ? (
                      <div className="grid gap-2 lg:grid-cols-[240px_minmax(0,1fr)]">
                        <ScrollArea className="h-80 rounded-md border">
                          <div className="flex flex-col">
                            {decompiled.files.map((f) => (
                              <button
                                key={f.path}
                                type="button"
                                disabled={!f.text}
                                onClick={() => void openFile(detail.sample.id, f.path)}
                                className={cn(
                                  "truncate border-b px-2 py-1.5 text-left font-mono text-[10px]",
                                  selectedFile === f.path ? "bg-muted" : "hover:bg-muted/60",
                                  !f.text && "cursor-not-allowed opacity-40",
                                )}
                                title={f.path}
                              >
                                {f.path}
                              </button>
                            ))}
                          </div>
                        </ScrollArea>
                        <div className="h-80 overflow-auto rounded-md border bg-muted/20">
                          {fileLoading ? (
                            <p className="p-3 text-muted-foreground text-xs">加载中…</p>
                          ) : (
                            <pre className="whitespace-pre-wrap break-all p-3 text-[11px] leading-relaxed">
                              {fileContent || "选择左侧文本文件查看源码"}
                            </pre>
                          )}
                        </div>
                      </div>
                    ) : (
                      <p className="text-muted-foreground text-xs">
                        {decompileRunning
                          ? "Ghidra 正在后台反编译（原生 PE 通常需要数分钟），完成后会自动刷新。"
                          : detail.sample.decompile_status === "done"
                            ? "未找到可展示的文本产物。"
                            : "点击右上角「反编译」生成源码/伪代码；.NET 需 ILSpy，Electron 需 asar，APK/JAR 需 jadx，原生 PE 需 Ghidra（异步执行）。"}
                      </p>
                    )}
                  </div>
                </>
              )}
            </CardContent>
          </Card>
        </div>
      ) : null}
    </div>
  );
}

function Info({ label, value }: { label: string; value?: string }) {
  return (
    <div className="rounded-md border px-2.5 py-1.5">
      <div className="text-[10px] text-muted-foreground">{label}</div>
      <div className="truncate font-medium text-xs" title={value}>
        {value || "—"}
      </div>
    </div>
  );
}
