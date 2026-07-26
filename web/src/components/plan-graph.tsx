"use client";

import { Background, Controls, ReactFlow, type Edge, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { StatusBadge } from "@/components/status-badge";

type GraphTask = { id: string; title: string; status: string; depth: number };
type Dependency = { task_id: string; depends_on_task_id: string; dependency_type: string };

export function PlanGraph({ tasks, dependencies }: { tasks: GraphTask[]; dependencies: Dependency[] }) {
  const positions = new Map<number, number>();
  const nodes: Node[] = tasks.map(task => {
    const row = positions.get(task.depth) ?? 0;
    positions.set(task.depth, row + 1);
    return {
      id: task.id,
      position: { x: task.depth * 280, y: row * 110 },
      data: { label: <div className="graph-node"><strong>{task.title}</strong><StatusBadge status={task.status} /></div> },
      className: "flow-node",
    };
  });
  const edges: Edge[] = dependencies.map(item => ({ id: `${item.depends_on_task_id}-${item.task_id}`, source: item.depends_on_task_id, target: item.task_id, label: item.dependency_type }));
  return <div className="graph"><ReactFlow nodes={nodes} edges={edges} fitView nodesDraggable={false} nodesConnectable={false}><Background /><Controls /></ReactFlow></div>;
}
