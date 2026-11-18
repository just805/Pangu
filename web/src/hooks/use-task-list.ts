"use client";

import * as React from "react";

import { api } from "@/lib/api";
import type { Task } from "@/lib/types";

// ── 共享任务列表 ────────────────────────────────────────────────────────────────
//
// 此前任务列表被两处独立轮询：tasks 页（10s，含签名去重与本地突变）与 dashboard
// （5s，只读统计）——同一份全量列表、两套 state、两个 interval。这里收敛为模块级
// 单例：第一个订阅者启动轮询，订阅清零即停；页面隐藏时暂停（恢复可见补一轮）；
// 所有订阅者拿到同一份数据、同一渲染节奏。
//
// tasks 页的交互语义通过两个出口保留：
//   - patchSharedTask：乐观更新单条任务（如行内改分类），本地立即生效，下一轮
//     轮询以服务端为准；
//   - refreshTaskList：本地操作（删除/暂停/改名/归档）后强制下一轮接收，否则
//     相同负载会被签名去重挡掉、UI 看起来没反应。

const POLL_MS = 10_000;

let tasks: Task[] = [];
let signature = "";
let timer: ReturnType<typeof setInterval> | null = null;
let inflight = false;
const listeners = new Set<React.Dispatch<React.SetStateAction<Task[]>>>();

function notify() {
	for (const listener of listeners) listener(tasks);
}

async function poll() {
	if (inflight) return;
	inflight = true;
	try {
		const r = await api.tasks();
		const next = r.tasks.map((t) => (t.id === r.active ? { ...t, active: true } : t));
		const sig = JSON.stringify(next);
		if (sig === signature) return;
		signature = sig;
		tasks = next;
		notify();
	} catch {
		// Polling is best-effort; the next interval retries automatically.
	} finally {
		inflight = false;
	}
}

function ensurePolling() {
	if (timer) return;
	void poll();
	timer = setInterval(() => {
		if (typeof document !== "undefined" && document.visibilityState !== "visible") return;
		void poll();
	}, POLL_MS);
}

function stopPollingIfUnused() {
	if (timer && listeners.size === 0) {
		clearInterval(timer);
		timer = null;
	}
}

// 订阅共享任务列表。首次订阅立即补一轮；订阅者全部离开后轮询停止。
export function useSharedTaskList(): Task[] {
	const [list, setList] = React.useState<Task[]>(tasks);
	React.useEffect(() => {
		listeners.add(setList);
		ensurePolling();
		if (listeners.size === 1) void poll();
		return () => {
			listeners.delete(setList);
			stopPollingIfUnused();
		};
	}, []);
	return list;
}

// 乐观更新单条任务并失效签名（下一轮即使负载相同也照单接收，以服务端为准）。
export function patchSharedTask(id: string, patch: Partial<Task>) {
	tasks = tasks.map((t) => (t.id === id ? { ...t, ...patch } : t));
	signature = "";
	notify();
}

// 本地操作后调用：清签名并立即拉一轮。
export function refreshTaskList() {
	signature = "";
	void poll();
}
