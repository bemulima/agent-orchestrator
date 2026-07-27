"use client";

import Link from "next/link";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { StatusBadge } from "@/components/status-badge";

type GraphTask = { id: string; title: string; status: string; depth: number };
type Dependency = { task_id: string; depends_on_task_id: string; dependency_type: string };
type TaskNodeData = { task: GraphTask; order: number; hasIncoming: boolean; hasOutgoing: boolean };
type TaskNode = Node<TaskNodeData, "task">;

const cardWidth = 300;
const columnGap = 120;
const rowGap = 34;
const rowHeight = 190;

function PlanTaskNode({ data }: NodeProps<TaskNode>) {
  return <>
    {data.hasIncoming && <Handle className="graph-task-handle" type="target" position={Position.Left} />}
    <Link className="graph-task-card nodrag nopan" href={`/tasks/${data.task.id}`} aria-label={`Открыть задачу ${data.order}: ${data.task.title}`}>
      <span className="graph-task-meta"><span>Задача {String(data.order).padStart(2, "0")}</span><span>Волна {data.task.depth}</span></span>
      <strong>{data.task.title}</strong>
      <span className="graph-task-footer"><StatusBadge status={data.task.status} /><span>Открыть →</span></span>
    </Link>
    {data.hasOutgoing && <Handle className="graph-task-handle" type="source" position={Position.Right} />}
  </>;
}

const nodeTypes = { task: PlanTaskNode };

export function PlanGraph({ tasks, dependencies }: { tasks: GraphTask[]; dependencies: Dependency[] }) {
  const tasksByDepth = new Map<number, GraphTask[]>();
  for (const task of tasks) tasksByDepth.set(task.depth, [...(tasksByDepth.get(task.depth) ?? []), task]);
  const widestWave = Math.max(1, ...Array.from(tasksByDepth.values(), wave => wave.length));
  const taskOrder = new Map(tasks.map((task, index) => [task.id, index + 1]));
  const incoming = new Set(dependencies.map(dependency => dependency.task_id));
  const outgoing = new Set(dependencies.map(dependency => dependency.depends_on_task_id));
  const rowPitch = rowHeight + rowGap;

  const nodes: TaskNode[] = [];
  for (const [depth, wave] of tasksByDepth) {
    const offset = ((widestWave - wave.length) * rowPitch) / 2;
    wave.forEach((task, row) => nodes.push({
      id: task.id,
      type: "task",
      position: { x: depth * (cardWidth + columnGap), y: offset + row * rowPitch },
      data: { task, order: taskOrder.get(task.id) ?? 0, hasIncoming: incoming.has(task.id), hasOutgoing: outgoing.has(task.id) },
      className: "flow-task-node",
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
    }));
  }

  const edges: Edge[] = dependencies.map(item => ({
    id: `${item.depends_on_task_id}-${item.task_id}`,
    source: item.depends_on_task_id,
    target: item.task_id,
    type: "smoothstep",
    markerEnd: { type: MarkerType.ArrowClosed, color: "var(--graph-edge)" },
    style: { stroke: "var(--graph-edge)", strokeWidth: 2 },
    ariaLabel: "Обязательную задачу нужно завершить до зависимой",
  }));
  const graphHeight = Math.max(420, Math.min(760, widestWave * rowPitch + 72));

  return <div className="graph" style={{ height: graphHeight }}>
    <ReactFlow<TaskNode, Edge>
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      fitView
      fitViewOptions={{ padding: 0.18, minZoom: 0.6, maxZoom: 1 }}
      minZoom={0.45}
      maxZoom={1.35}
      nodesDraggable={false}
      nodesConnectable={false}
      ariaLabelConfig={{
        "controls.ariaLabel": "Управление масштабом",
        "controls.zoomIn.ariaLabel": "Увеличить",
        "controls.zoomOut.ariaLabel": "Уменьшить",
        "controls.fitView.ariaLabel": "Показать всю схему",
      }}
    >
      <Background color="var(--graph-dot)" gap={24} size={1.2} />
      <Controls aria-label="Управление масштабом" orientation="horizontal" showInteractive={false} />
    </ReactFlow>
  </div>;
}
