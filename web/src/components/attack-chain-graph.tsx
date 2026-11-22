"use client";

import * as React from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  MiniMap,
  type Edge as RFEdge,
  type Node as RFNode,
  ReactFlow,
  ReactFlowProvider,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";

import type { AttackChain, AttackChainStep, ChainRel } from "@/lib/types";
import { cn } from "@/lib/utils";

const relLabel: Record<ChainRel, string> = {
  derived_from: "显式前置",
  enables: "推进",
  chained_with: "同源并报",
};

const relColor: Record<ChainRel, string> = {
  derived_from: "#6366f1",
  enables: "#f59e0b",
  chained_with: "#14b8a6",
};

const sevStyle: Record<string, { chip: string; border: string }> = {
  critical: { chip: "bg-rose-500/15 text-rose-600 dark:text-rose-400", border: "#e11d48" },
  high: { chip: "bg-orange-500/15 text-orange-600 dark:text-orange-400", border: "#ea580c" },
  medium: { chip: "bg-amber-500/15 text-amber-600 dark:text-amber-400", border: "#ca8a04" },
  low: { chip: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400", border: "#16a34a" },
};

const sevCN: Record<string, string> = {
  critical: "严重",
  high: "高危",
  medium: "中危",
  low: "低危",
};

function stepNode(step: AttackChainStep, index: number): RFNode {
  const style = sevStyle[step.severity] ?? { chip: "bg-muted text-muted-foreground", border: "#6b7280" };
  return {
    id: String(step.finding_id),
    position: { x: (index % 3) * 320, y: Math.floor(index / 3) * 170 },
    data: {
      label: (
        <div className="w-[248px] text-left">
          <div className="mb-1.5 flex items-center gap-1.5">
            <span className="inline-flex size-5 items-center justify-center rounded-full bg-foreground/10 font-semibold text-[11px]">
              {step.seq}
            </span>
            <span className={cn("rounded px-1.5 py-0.5 font-medium text-[10px]", style.chip)}>
              {sevCN[step.severity] ?? step.severity ?? "未知"}
            </span>
            <span className="ml-auto text-[10px] text-muted-foreground">证明点 {step.proofs.length}</span>
          </div>
          <div className="line-clamp-2 font-medium text-xs leading-snug" title={step.title}>
            {step.title || step.vuln_class || "未命名发现"}
          </div>
          <div className="mt-1 line-clamp-1 text-[10px] text-muted-foreground">{step.intent_summary || step.vuln_class}</div>
        </div>
      ),
    },
    style: {
      width: 276,
      padding: "10px 12px",
      borderRadius: 10,
      border: `2px solid ${style.border}`,
      background: "var(--background)",
      color: "var(--foreground)",
      boxShadow: "0 1px 2px rgba(0,0,0,.06)",
    },
  };
}

export function AttackChainGraph({
  chain,
  className,
  selectedFindingId,
  onSelect,
}: {
  chain: AttackChain;
  className?: string;
  selectedFindingId?: number;
  onSelect?: (step: AttackChainStep) => void;
}) {
  const byFinding = React.useMemo(
    () => new Map(chain.steps.map((s) => [s.finding_id, s])),
    [chain.steps],
  );
  const nodes = React.useMemo(
    () =>
      chain.steps.map((step, index) => {
        const node = stepNode(step, index);
        if (selectedFindingId === step.finding_id) {
          return { ...node, style: { ...node.style, boxShadow: "0 0 0 3px rgba(99,102,241,.35)" } };
        }
        return node;
      }),
    [chain.steps, selectedFindingId],
  );
  const edges = React.useMemo<RFEdge[]>(
    () =>
      chain.edges
        .filter((e) => byFinding.has(e.src) && byFinding.has(e.dst))
        .map((e, i) => ({
          id: `e-${e.src}-${e.dst}-${e.rel}-${i}`,
          source: String(e.src),
          target: String(e.dst),
          label: relLabel[e.rel] ?? e.rel,
          type: "smoothstep",
          animated: e.rel === "enables",
          markerEnd: { type: MarkerType.ArrowClosed, color: relColor[e.rel] ?? "#94a3b8" },
          style: { stroke: relColor[e.rel] ?? "#94a3b8", strokeWidth: 1.8 },
          labelStyle: { fontSize: 10, fill: relColor[e.rel] ?? "#94a3b8" },
          labelBgStyle: { fill: "var(--background)", fillOpacity: 0.9 },
        })),
    [chain.edges, byFinding],
  );

  return (
    <div className={cn("relative h-[440px] w-full overflow-hidden rounded-lg border", className)}>
      <ReactFlowProvider>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          fitViewOptions={{ padding: 0.2 }}
          minZoom={0.25}
          maxZoom={1.4}
          nodesDraggable
          nodesConnectable={false}
          elementsSelectable
          onNodeClick={(_, node) => {
            const step = byFinding.get(Number(node.id));
            if (step) onSelect?.(step);
          }}
        >
          <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
          <Controls showInteractive={false} />
          <MiniMap pannable zoomable nodeStrokeWidth={2} />
        </ReactFlow>
      </ReactFlowProvider>
    </div>
  );
}
