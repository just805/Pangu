"use client";

import * as React from "react";

// ── 轮询基础设施 ────────────────────────────────────────────────────────────────
//
// 背景：全库 30+ 处 useEffect + setInterval 手动轮询，页面切后台后照常打满后端
// （渗透平台控制台常常长开挂机，后台标签页的请求是纯浪费），且部分 effect 的
// 依赖数组里放多个 useCallback，任一变化就销毁重建 interval、打断轮询节奏。
//
// usePageVisible + usePollInterval 是统一出口：
//   - 页面隐藏时暂停轮询（保留已有数据，恢复可见立即刷新一轮）；
//   - cb 存 ref——调用方无需 useCallback 包裹，依赖变化不打断节奏；
//   - deps 由调用方显式传入（通常 [taskId] 一类身份参数），变化时重建并立即执行。

// usePageVisible 报告页面是否可见（visibilitychange 订阅）。SSR/静态导出首帧按可见处理。
export function usePageVisible(): boolean {
	const [visible, setVisible] = React.useState<boolean>(() =>
		typeof document === "undefined" ? true : document.visibilityState === "visible",
	);
	React.useEffect(() => {
		const onChange = () => setVisible(document.visibilityState === "visible");
		document.addEventListener("visibilitychange", onChange);
		return () => document.removeEventListener("visibilitychange", onChange);
	}, []);
	return visible;
}

// usePollInterval 在页面可见时按 ms 周期执行 cb：
//   - 挂载与「恢复可见」时立即执行一轮（immediate: false 关闭）；
//   - 页面隐藏时暂停，恢复可见立即补一轮；
//   - 卸载与 deps 变化时清理重建。
// 竞态防护（旧响应覆盖新响应）由调用方的请求序号/alive 模式负责，本 hook 不重复。
export function usePollInterval(
	cb: () => void,
	ms: number,
	deps: React.DependencyList = [],
	opts: { immediate?: boolean } = {},
): void {
	const cbRef = React.useRef(cb);
	React.useEffect(() => {
		cbRef.current = cb;
	});
	const visible = usePageVisible();
	const immediate = opts.immediate !== false;
	React.useEffect(() => {
		if (!visible) return;
		if (immediate) cbRef.current();
		const id = setInterval(() => cbRef.current(), ms);
		return () => clearInterval(id);
		// deps 由调用方显式控制（身份参数，如 taskId）；cb 经 ref 转发不参与依赖。
	}, [visible, ms, immediate, ...deps]);
}
