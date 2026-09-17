"use client";

import { Background, Controls, MiniMap, ReactFlow, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { ArchitectureCurrent } from "@/lib/schemas";

export function architectureGraphItems(model: ArchitectureCurrent): { nodes: Node[]; edges: Edge[] } {
  const columns = Math.max(1, Math.ceil(Math.sqrt(model.services.length)));
  const nodes = model.services.map((service, index) => ({ id: service.project_id, position: { x: (index % columns) * 270, y: Math.floor(index / columns) * 160 }, data: { label: `${service.name}\n${service.service_kind} · ${service.drift_status}` }, style: { width: 210, whiteSpace: "pre-line" }, className: "architecture-node" }));
  const ids = new Set(nodes.map(node => node.id));
  const seen = new Set<string>();
  const edges = model.relations.flatMap(relation => { const key = `${relation.source_project_id}:${relation.target_project_id}:${relation.relation_type}:${relation.contract_code ?? ""}`; if (!ids.has(relation.source_project_id) || !ids.has(relation.target_project_id) || seen.has(key)) return []; seen.add(key); return [{ id: relation.id || key, source: relation.source_project_id, target: relation.target_project_id, label: [relation.relation_type, relation.protocol, relation.contract_code].filter(Boolean).join(" · "), type: "smoothstep" }]; });
  return { nodes, edges };
}

export function ArchitectureGraph({ model, onService }: { model: ArchitectureCurrent; onService: (id: string) => void }) {
  const { nodes, edges } = architectureGraphItems(model);
  return <div className="graph architecture-graph"><ReactFlow nodes={nodes} edges={edges} fitView minZoom={0.2} maxZoom={1.5} nodesDraggable nodesConnectable={false} elementsSelectable onNodeClick={(_, node) => onService(node.id)} onConnect={() => undefined} deleteKeyCode={null} aria-label="Карта текущей архитектуры"><Background /><Controls showInteractive={false} /><MiniMap zoomable pannable /></ReactFlow></div>;
}
