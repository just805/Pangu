// 共享格式化函数（R4 下沉：此前 fmtTokens 在 sessions-tab / 会话组件里重复实现）。

// fmtTokens 把 token 数压成人类可读格式：159M / 144M / 3.8M / 12.4k / 999。
export function fmtTokens(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${Math.round(n / 1e6)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
}
