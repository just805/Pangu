"use client";

import * as React from "react";

import dynamic from "next/dynamic";

import { api } from "@/lib/api";
import type { Edge, TaskNode } from "@/lib/types";

// 同 graph-tab：攻击链路图画布拆独立 chunk（@xyflow/react 较重），ssr:false。
const ExplorationGraph = dynamic(
  () => import("@/components/exploration-graph").then((m) => ({ default: m.ExplorationGraph })),
  { ssr: false },
);

// FindingLineageView renders the exploration sub-graph from the task's initial
// node down to this finding's node — the same 攻击链路图 canvas as the task graph,
// scoped to just this finding's lineage.
export function FindingLineageView({ findingId }: { findingId: string }) {
  const [nodes, setNodes] = React.useState<TaskNode[]>([]);
  const [edges, setEdges] = React.useState<Edge[]>([]);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    api
      .findingLineage(findingId)
      .then((g) => {
        if (!alive) return;
        setNodes(g.nodes ?? []);
        setEdges(g.edges ?? []);
      })
      .catch(() => {})
      .finally(() => {
        if (alive) setLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [findingId]);

  if (loaded && nodes.length === 0) {
    return (
      <p className="p-6 text-muted-foreground text-sm">
        无链路可展示（该漏洞未关联探索节点，或所属任务已删除）。
      </p>
    );
  }

  return (
    <ExplorationGraph
      nodes={nodes}
      edges={edges}
      className="h-[68vh]"
      emptyHint={loaded ? "无链路" : "加载中…"}
    />
  );
}
