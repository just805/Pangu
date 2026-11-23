"use client";

import { useEffect, useRef, useState } from "react";

import { useRouter } from "next/navigation";

import { AlertTriangle, ArrowRight, Eye, EyeOff, LockKeyhole, ShieldCheck, ShieldHalf } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";
import { MOCK } from "@/lib/mock/enabled";

// 认证页防指纹约定（公网部署场景）：
//   1. 文案与结构不出现产品名、不引用 /logo.png（图片哈希最容易被测绘）；
//      顶部几何标记是内联 SVG，随主题变化、无独立资源。
//   2. v0.4.43 视觉升级：双层动画光晕背景 + 玻璃拟态卡片 + 入场动画 +
//      微交互（输入聚焦光晕、按钮渐变），全部纯 CSS 不引入外部资源。
export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  const [agreed, setAgreed] = useState(false);
  const [termsOpen, setTermsOpen] = useState(false);
  const [readToEnd, setReadToEnd] = useState(false);
  const [focused, setFocused] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const termsBodyRef = useRef<HTMLDivElement>(null);

  function handleTermsScroll() {
    const el = termsBodyRef.current;
    if (!el) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) setReadToEnd(true);
  }

  useEffect(() => {
    if (!termsOpen) return;
    setReadToEnd(false);
    const el = termsBodyRef.current;
    if (el && el.scrollHeight <= el.clientHeight + 8) setReadToEnd(true);
  }, [termsOpen]);

  useEffect(() => {
    if (MOCK) {
      // Mock demo：无真实登录，直接进主界面。
      window.location.replace("/function/tasks");
      return;
    }
    // 已登录检测走服务端探活：token 在 HttpOnly cookie 里（前端读不到），能通过
    // 鉴权端点（/api/version 需有效 JWT）即视为已登录，直接回主界面。
    fetch("/api/version", { credentials: "same-origin" })
      .then((r) => {
        if (r.ok) {
          window.location.replace("/function/tasks");
          return;
        }
        return api
          .authStatus()
          .then(({ initialized }) => {
            if (!initialized) router.replace("/setup");
          })
          .catch(() => setError("无法连接到后端服务"))
          .finally(() => setChecking(false));
      })
      .catch(() => {
        // /api/version 网络失败（后端未启动）时仍要让 authStatus 试一次给出明确报错。
        api
          .authStatus()
          .then(({ initialized }) => {
            if (!initialized) router.replace("/setup");
          })
          .catch(() => setError("无法连接到后端服务"))
          .finally(() => setChecking(false));
      });
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!agreed) {
      setError("请先阅读并同意《安全与合规声明》");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("PANGU", password);
      auth.setToken(token);
      window.location.replace("/function/tasks");
    } catch (e) {
      const err = e as Error & { status?: number };
      if (!err.status) {
        setError("无法连接到后端服务，请确认服务已启动");
      } else if (err.status === 429) {
        setError(err.message || "尝试过于频繁，账号已临时锁定，请稍后再试");
      } else if (err.status === 403 && err.message.includes("未初始化")) {
        setError("密码尚未初始化，请先完成首次设置");
      } else if (err.status === 401) {
        setError("用户名或密码错误");
      } else {
        setError(err.message || "登录失败，请稍后再试");
      }
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div role="status" className="flex min-h-dvh items-center justify-center text-muted-foreground">
        <div className="flex items-center gap-3">
          <ShieldHalf className="size-5 animate-pulse text-primary" />
          正在检查登录状态…
        </div>
      </div>
    );
  }

  return (
    <div className="relative flex min-h-dvh items-center justify-center overflow-hidden p-4 sm:p-6">
      {/* 背景：细网格 + 双色光晕，全部内联，无外部资源 */}
      <div aria-hidden className="pointer-events-none absolute inset-0">
        <div className="absolute inset-0 bg-background" />
        <div
          className="absolute inset-0 opacity-[0.16]"
          style={{
            backgroundImage:
              "linear-gradient(to right, hsl(var(--border)) 1px, transparent 1px), linear-gradient(to bottom, hsl(var(--border)) 1px, transparent 1px)",
            backgroundSize: "52px 52px",
            maskImage: "radial-gradient(ellipse 70% 60% at 50% 45%, black 15%, transparent 75%)",
            WebkitMaskImage: "radial-gradient(ellipse 70% 60% at 50% 45%, black 15%, transparent 75%)",
          }}
        />
        <div
          className="absolute -top-[35%] left-[60%] h-[70vh] w-[70vw] -translate-x-1/2 rounded-full opacity-[0.08]"
          style={{ background: "radial-gradient(ellipse, hsl(var(--primary)) 0%, transparent 70%)" }}
        />
        <div
          className="absolute -bottom-[30%] left-[35%] h-[60vh] w-[60vw] -translate-x-1/2 rounded-full opacity-[0.06]"
          style={{ background: "radial-gradient(ellipse, hsl(210 90% 55%) 0%, transparent 70%)" }}
        />
      </div>

      <div className="relative w-full max-w-4xl animate-in fade-in slide-in-from-bottom-6 duration-700">
        <div className="grid overflow-hidden rounded-3xl border bg-card/85 shadow-2xl backdrop-blur-xl lg:grid-cols-[1.05fr_1fr]">
          {/* 左侧抽象安全图形：内联绘制，不引用独立图片资源 */}
          <div className="relative hidden flex-col justify-between overflow-hidden border-r bg-linear-to-br from-primary/10 via-background to-background p-10 lg:flex">
            <div
              aria-hidden
              className="pointer-events-none absolute inset-0 flex items-center justify-center overflow-hidden"
            >
              <span className="select-none font-semibold text-[9rem] text-primary/[0.04] tracking-[0.18em]">盘古</span>
            </div>
            <BrandMark />

            <div className="relative flex flex-1 items-center justify-center py-10">
              <div className="absolute size-72 rounded-full border border-primary/10" />
              <div className="absolute size-52 rounded-full border border-primary/15" />
              <div className="absolute size-36 rounded-full border border-primary/20" />
              <div
                aria-hidden
                className="absolute size-72 rounded-full border border-primary/10"
                style={{ animation: "ping 4s cubic-bezier(0,0,0.2,1) infinite" }}
              />
              <div className="relative flex size-24 items-center justify-center rounded-2xl border border-primary/25 bg-primary/10 shadow-lg shadow-primary/10">
                <ShieldCheck className="size-11 text-primary" />
              </div>
              <span className="absolute left-[18%] top-[24%] size-1.5 rounded-full bg-emerald-500/80" />
              <span className="absolute right-[20%] top-[36%] size-1 rounded-full bg-primary/60" />
              <span className="absolute bottom-[24%] left-[30%] size-1 rounded-full bg-primary/50" />
            </div>

            <div className="space-y-2">
              <div className="font-mono text-[10px] uppercase tracking-[0.32em] text-muted-foreground">
                Authorized Access Only
              </div>
              <p className="font-medium text-base">授权访问 · 全程留痕</p>
              <p className="text-muted-foreground text-xs leading-relaxed">
                本入口仅用于授权运维与安全评估，所有操作受审计与拦截策略约束。
              </p>
            </div>
          </div>

          {/* 右侧登录表单 */}
          <div className="p-7 sm:p-10">
            <div className="mb-8">
              <div className="mb-6 lg:hidden">
                <BrandMark compact />
              </div>
              <div className="inline-flex items-center gap-2 rounded-full border bg-muted/40 px-3 py-1 text-[11px] text-muted-foreground">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                授权访问
              </div>
              <h1 className="mt-4 font-semibold text-2xl tracking-tight">控制台登录</h1>
              <p className="mt-1.5 text-muted-foreground text-sm">仅限授权运维人员访问</p>
            </div>

            <form onSubmit={handleSubmit} className="flex flex-col gap-5">
              {/* 固定账户不在界面展示；保留隐藏字段以兼容密码管理器 */}
              <input type="text" name="username" value="PANGU" autoComplete="username" readOnly hidden />

              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label htmlFor="password" className="text-muted-foreground text-xs">密码</Label>
                  <span className="text-[10px] text-muted-foreground/70">区分大小写</span>
                </div>
                <div className="relative">
                  <LockKeyhole className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground/70" />
                  <Input
                    id="password"
                    type={showPassword ? "text" : "password"}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="请输入登录密码"
                    autoFocus
                    autoComplete="current-password"
                    onFocus={() => setFocused(true)}
                    onBlur={() => setFocused(false)}
                    className="h-11 border-border/60 pr-11 pl-10 transition-shadow duration-200"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((v) => !v)}
                    aria-label={showPassword ? "隐藏密码" : "显示密码"}
                    className="absolute top-1/2 right-1 flex size-9 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  >
                    {showPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                  </button>
                  <div
                    aria-hidden
                    className={`absolute -bottom-px left-1/2 h-px -translate-x-1/2 bg-primary transition-all duration-300 ${
                      focused ? "w-full opacity-100" : "w-0 opacity-0"
                    }`}
                  />
                </div>
              </div>

              <div className="flex items-start gap-2">
                <Checkbox
                  id="agree-terms"
                  checked={agreed}
                  onCheckedChange={(v) => setAgreed(v === true)}
                  className="mt-0.5"
                />
                <Label htmlFor="agree-terms" className="font-normal text-muted-foreground text-sm leading-relaxed">
                  我已阅读并同意
                  <button
                    type="button"
                    onClick={() => setTermsOpen(true)}
                    className="mx-0.5 font-medium text-primary underline-offset-4 hover:underline"
                  >
                    《安全与合规声明》
                  </button>
                </Label>
              </div>

              {error && (
                <p
                  role="alert"
                  className="flex animate-in items-start gap-2 rounded-lg border border-destructive/20 bg-destructive/5 px-3 py-2 text-destructive text-xs leading-relaxed fade-in slide-in-from-top-1"
                >
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                  {error}
                </p>
              )}

              <Button
                type="submit"
                className="group h-11 w-full bg-linear-to-r from-primary to-primary/85 font-medium tracking-wide shadow-sm transition-all duration-200 hover:shadow-lg hover:brightness-105"
                disabled={loading || !password || !agreed}
              >
                {loading ? (
                  <span className="flex items-center gap-2">
                    <span className="inline-block size-3 animate-spin rounded-full border-2 border-current border-t-transparent" />
                    登录中…
                  </span>
                ) : (
                  <span className="flex items-center gap-2">
                    登 录
                    <ArrowRight className="size-4 transition-transform duration-200 group-hover:translate-x-0.5" />
                  </span>
                )}
              </Button>
            </form>

            <div className="mt-6 flex flex-wrap items-center justify-between gap-2 border-t pt-4 text-[10px] text-muted-foreground">
              <span>连续登录失败会触发临时锁定</span>
              <span className="inline-flex items-center gap-1">
                <ShieldCheck className="size-3.5 text-emerald-500" />
                凭据仅通过受保护通道传输
              </span>
            </div>
          </div>
        </div>
      </div>

      <Dialog open={termsOpen} onOpenChange={setTermsOpen}>
        <DialogContent className="gap-0 p-0 sm:max-w-xl">
          <DialogHeader className="flex-row items-center gap-3 border-b px-6 py-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-destructive/10 text-destructive">
              <AlertTriangle className="size-5" />
            </div>
            <DialogTitle className="text-base">安全与合规声明</DialogTitle>
          </DialogHeader>
          <div
            ref={termsBodyRef}
            onScroll={handleTermsScroll}
            className="max-h-[55vh] space-y-4 overflow-y-auto px-6 py-5 text-muted-foreground text-sm leading-relaxed"
          >
            <p>
              本系统仅供授权人员在合法授权范围内开展安全评估工作。登录即表示您确认已理解并接受以下约束：
            </p>
            <ul className="ml-5 list-disc space-y-2">
              <li>
                仅可对**已获得明确书面授权**的目标开展测试；对任何未授权系统发起扫描、探测或利用均属违规乃至违法行为；
              </li>
              <li>
                本系统具备在服务器上执行命令的能力——请妥善保管访问凭据，凭据泄露等同于服务器控制权泄露；
              </li>
              <li>
                系统内置拦截与审批机制用于降低误操作风险，但不构成对使用合法性的担保；使用产生的全部法律责任由使用者自行承担；
              </li>
              <li>严禁利用本系统从事任何违反所在国家或地区法律法规的活动。</li>
            </ul>
          </div>
          <DialogFooter className="mx-0 mb-0 flex-col items-stretch gap-2 rounded-b-xl px-6 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-muted-foreground text-xs">
              {readToEnd ? "您已浏览全部条款" : "请将条款滚动至底部后再确认"}
            </p>
            <DialogClose asChild>
              <Button
                type="button"
                disabled={!readToEnd}
                onClick={() => {
                  setAgreed(true);
                  setError("");
                }}
              >
                我已阅读并同意
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// 品牌标识：盾牌 + “盘古 / PANGU”。全部内联，避免独立图片资源被测绘。
function BrandMark({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-2.5">
      <div className="flex size-9 items-center justify-center rounded-xl border border-primary/20 bg-primary/10 shadow-sm">
        <ShieldHalf className="size-4 text-primary" />
      </div>
      <div className="leading-tight">
        <div className="flex items-baseline gap-2">
          <span className="font-semibold text-base tracking-[0.2em]">盘古</span>
          <span className="font-mono text-[9px] uppercase tracking-[0.24em] text-muted-foreground">PANGU</span>
        </div>
        {compact ? null : <div className="mt-0.5 text-[10px] text-muted-foreground">安全控制台</div>}
      </div>
    </div>
  );
}
