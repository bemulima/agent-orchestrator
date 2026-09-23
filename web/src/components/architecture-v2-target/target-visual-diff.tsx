"use client";

import { Background, Controls, MiniMap, ReactFlow, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useMemo } from "react";
import type { ArchitectureCatalog } from "@/lib/schemas";
import type { TargetChangeDraft } from "./types";
import { targetVisualDiffModel } from "./target-adapter";

const tones = {
  context: { border: "#64748b", background: "#f8fafc" },
  add: { border: "#16a34a", background: "#f0fdf4" },
  change: { border: "#2563eb", background: "#eff6ff" },
  remove: { border: "#dc2626", background: "#fef2f2" },
};

export function TargetVisualDiff({ catalog, changes }: { catalog: ArchitectureCatalog; changes: TargetChangeDraft[] }) {
  const graph = useMemo(() => targetVisualDiffModel(catalog, changes), [catalog, changes]);
  const nodes: Node[] = graph.nodes.map((node, index) => ({
    id: node.id,
    position: { x: (index % 3) * 265, y: Math.floor(index / 3) * 175 },
    data: { label: <div><strong>{node.label}</strong><br /><small>{node.subtitle}</small>{node.operationLabels.slice(0, 2).map(operation => <small key={operation} style={{ display: "block", marginTop: 5 }}>↳ {operation}</small>)}</div> },
    style: { width: 225, border: `2px solid ${tones[node.change].border}`, background: tones[node.change].background, borderRadius: 10, padding: 10 },
  }));
  const edges: Edge[] = graph.edges.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, label: edge.label, type: "smoothstep", style: { stroke: "#64748b" }, labelStyle: { fontSize: 10, fill: "currentColor" } }));
  if (!nodes.length) return <section className="panel"><h3>Visual target changes</h3><p className="muted">Выберите service или operation для visual diff. CURRENT остаётся read-only.</p></section>;
  return <section className="panel" aria-label="Visual target diff" style={{ minHeight: 360 }}><header><p className="eyebrow">VISUAL DIFF</p><h3>Контекст изменяемой архитектуры</h3><p><span style={{ color: tones.add.border }}>Added</span> · <span style={{ color: tones.change.border }}>Changed</span> · <span style={{ color: tones.remove.border }}>Removed</span> · Context</p></header><div className="graph" style={{ height: 300 }}><ReactFlow nodes={nodes} edges={edges} fitView nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} deleteKeyCode={null} aria-label="Target visual diff graph"><Background /><Controls showInteractive={false} /><MiniMap /></ReactFlow></div></section>;
}
