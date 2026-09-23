"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  applyNodeChanges,
  type Edge,
  type Node,
  type NodeChange,
  type ReactFlowInstance,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  allOperations,
  classification,
  groupOperations,
  list,
  operationID,
  operationIdentity,
  operationSlug,
  serviceID,
  serviceName,
  serviceSlug,
  text,
  transport,
  type V2Catalog,
  type V2Mode,
  type V2Operation,
  type V2Relation,
  type V2Service,
  visibleServices,
} from "./model";
import { orthogonalEdgeTypes } from "./orthogonal-edge";
import {
  ArchitectureTargetDrawer,
  targetDrawerContextDraft,
} from "@/components/architecture-v2-target/architecture-target-drawer";
import type { ArchitectureCatalog, ArchitectureTarget } from "@/lib/schemas";
import { architectureTargetResponseSchema } from "@/lib/schemas";
import { apiPost } from "@/lib/api";
import type {
  TargetChangeDraft,
  TargetLifecycleAction,
} from "@/components/architecture-v2-target";

type Inspector = {
  kind:
    | "platform"
    | "service"
    | "relation"
    | "group"
    | "unlinked"
    | "operation"
    | "flow";
  value?: any;
};
type CanvasNodeData = {
  title: string;
  subtitle?: string;
  kind: string;
  value?: Inspector;
  dimmed?: boolean;
};
type CanvasGraph = {
  key: string;
  signature: string;
  nodes: Node<CanvasNodeData>[];
  edges: Edge[];
  initialFitNodeIDs?: string[];
};
type FocusRequest = { id: string; nonce: number };
const NODE_WIDTH = 286,
  NODE_HEIGHT = 94,
  referenceService = "ms-go-linux-validator",
  referenceOperation = "validate";

async function readCatalog(): Promise<V2Catalog> {
  const response = await fetch("/api/v1/architecture/platform", {
    headers: { Accept: "application/json" },
    cache: "no-store",
  });
  if (!response.ok)
    throw new Error(`CURRENT catalog unavailable: HTTP ${response.status}`);
  return response.json();
}
function statement(value: unknown) {
  return text(value) || "unknown";
}
function evidence(value: unknown) {
  const items = list<any>(value);
  return items.length
    ? items
        .map(
          (item) =>
            [item.source_path, item.symbol].filter(Boolean).join(" · ") ||
            "evidence",
        )
        .join("; ")
    : "unknown";
}
function confidence(value: unknown) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? `${Math.round(number * 100)}%` : "unknown";
}
function relationLabel(relation: any) {
  return [
    String(relation.transport || "relation").toUpperCase(),
    relation.contract || relation.contract_code,
    relation.direction,
  ]
    .filter(Boolean)
    .join(" · ");
}
function relationResolution(relation: any, services: V2Service[]) {
  if (
    relation.target_project_id &&
    services.some(
      (service) => serviceID(service) === relation.target_project_id,
    )
  )
    return "Confirmed internal relation";
  const target = String(relation.external_target || "");
  if (!target) return "Unknown counterpart";
  if (
    services.some(
      (service) =>
        serviceName(service) === target || serviceID(service) === target,
    )
  )
    return "Unresolved internal reference";
  if (/^unknown\b/i.test(target)) return "Unknown counterpart";
  return "Declared external target";
}
function routeForService(service: V2Service) {
  return `/architecture-v2/current/services/${serviceSlug(service)}`;
}
function routeForOperation(service: V2Service, operation: V2Operation) {
  return `${routeForService(service)}/operations/${operationSlug(operation)}`;
}
function searchText(value: string) {
  return value.toLowerCase().replace(/[-_]+/g, " ").replace(/\s+/g, " ").trim();
}
function edgeKind(relation: any) {
  // Transport alone is not sufficient: both NATS request/reply and published
  // events use `nats` in the catalog. The catalog's proven relation type keeps
  // their visual semantics distinct without inferring either from a name.
  const relationType = String(
    relation?.type || relation?.relation_type || "",
  ).toLowerCase();
  const transport = String(
    relation?.transport || relation?.protocol || "",
  ).toLowerCase();
  if (/event|publish|subscribe|queue/.test(relationType)) return "event";
  if (/event|publish|subscribe|queue/.test(transport)) return "event";
  if (/nats/.test(transport)) return "nats";
  return "http";
}
function edgeStyle(kind: string, dimmed = false) {
  const colors: Record<string, string> = {
    http: "#236449",
    nats: "#256e91",
    event: "#8255a8",
    data: "#3e669f",
    external: "#704b94",
    outcome: "#236449",
    annotation: "#7a817b",
    owns: "#3e669f",
    contains: "#7a817b",
  };
  return {
    stroke: colors[kind] || "#68736b",
    strokeWidth: dimmed ? 1 : 1.8,
    opacity: dimmed ? 0.18 : 1,
    strokeDasharray:
      kind === "event"
        ? "9 4 2 4"
        : [
              "nats",
              "data",
              "external",
              "annotation",
              "owns",
              "contains",
            ].includes(kind)
          ? "5 4"
          : undefined,
  };
}
function semanticEdge(
  id: string,
  source: string,
  target: string,
  label: string,
  kind: string,
  relation?: any,
  dimmed = false,
  parallelIndex = 0,
  parallelCount = 1,
): Edge {
  const directional = !["annotation", "owns", "contains"].includes(kind);
  return {
    id,
    source,
    target,
    label,
    type: "orthogonal",
    markerEnd: directional ? { type: MarkerType.ArrowClosed } : undefined,
    style: edgeStyle(kind, dimmed),
    data: { kind, relation, directional, parallelIndex, parallelCount },
    interactionWidth: 26,
  };
}

function ArchitectureNode({
  id,
  data,
  selected,
}: {
  id: string;
  data: CanvasNodeData;
  selected?: boolean;
}) {
  const icon: Record<string, string> = {
    service: "◈",
    group: "▦",
    operation: "⇢",
    resource: "▤",
    external: "◌",
    process: "◆",
    decision: "◇",
    annotation: "i",
    input: "↪",
    output: "↩",
    error: "!",
  };
  const handles = [
    Position.Left,
    Position.Right,
    Position.Top,
    Position.Bottom,
  ];
  return (
    <div
      data-testid={`architecture-node-${id}`}
      className={`architecture-v2-node architecture-v2-node-${data.kind} ${selected ? "is-selected" : ""} ${data.dimmed ? "is-dimmed" : ""}`}
    >
      {handles.map((position) => (
        <Handle
          key={`target-${position}`}
          id={`target-${position}`}
          type="target"
          position={position}
          className="architecture-v2-handle"
        />
      ))}
      <span className="architecture-v2-node-icon" aria-hidden>
        {icon[data.kind] || "·"}
      </span>
      <div>
        <strong>{data.title}</strong>
        {data.subtitle && <small>{data.subtitle}</small>}
      </div>
      {handles.map((position) => (
        <Handle
          key={`source-${position}`}
          id={`source-${position}`}
          type="source"
          position={position}
          className="architecture-v2-handle"
        />
      ))}
    </div>
  );
}
const nodeTypes = { architecture: ArchitectureNode };
const edgeTypes = orthogonalEdgeTypes;

function layoutKey(fingerprint: string, graphKey: string) {
  return `architecture-v2-layout:v2:${fingerprint || "unknown"}:${graphKey}`;
}
// v2 deliberately ignores the pre-free-layout viewport while retaining it in
// localStorage for rollback; it was calibrated for parent-bounded nodes.
function viewportKey(fingerprint: string, graphKey: string) {
  return `architecture-v2-viewport:v2:${fingerprint || "unknown"}:${graphKey}`;
}
function collapsedGroupsKey(fingerprint: string, id: string) {
  return `architecture-v2-groups:v2:${fingerprint || "unknown"}:${id}`;
}
function readCollapsedGroups(fingerprint: string, id: string) {
  if (typeof window === "undefined") return [] as string[];
  try {
    const value = JSON.parse(
      window.localStorage.getItem(collapsedGroupsKey(fingerprint, id)) || "[]",
    );
    return Array.isArray(value)
      ? value.filter((item): item is string => typeof item === "string")
      : [];
  } catch {
    return [];
  }
}
function readPositions(key: string) {
  if (typeof window === "undefined" || !key)
    return {} as Record<string, { x: number; y: number }>;
  try {
    return JSON.parse(window.localStorage.getItem(key) || "{}") as Record<
      string,
      { x: number; y: number }
    >;
  } catch {
    return {};
  }
}
function restoredNodes(nodes: Node<CanvasNodeData>[], key: string) {
  const positions = readPositions(key);
  return nodes.map((node) =>
    positions[node.id] ? { ...node, position: positions[node.id] } : node,
  );
}
function overlaps(a: Node<CanvasNodeData>, b: Node<CanvasNodeData>) {
  return (
    a.position.x < b.position.x + NODE_WIDTH &&
    a.position.x + NODE_WIDTH > b.position.x &&
    a.position.y < b.position.y + NODE_HEIGHT &&
    a.position.y + NODE_HEIGHT > b.position.y
  );
}
function resolveOverlap(
  dragged: Node<CanvasNodeData>,
  nodes: Node<CanvasNodeData>[],
) {
  let candidate = { ...dragged, position: { ...dragged.position } },
    attempts = 0;
  while (
    nodes.some(
      (node) => node.id !== candidate.id && overlaps(candidate, node),
    ) &&
    attempts < 60
  ) {
    candidate = {
      ...candidate,
      position: {
        x: candidate.position.x,
        y: candidate.position.y + NODE_HEIGHT + 26,
      },
    };
    attempts += 1;
  }
  return candidate;
}

function ArchitectureCanvas({
  fingerprint,
  graph,
  onInspector,
  onDrillDown,
  onClearInspector,
  onToggleGroup,
  focusRequest,
}: {
  fingerprint: string;
  graph: CanvasGraph;
  onInspector: (value: Inspector) => void;
  onDrillDown: (value: Inspector) => void;
  onClearInspector: () => void;
  onToggleGroup: (value: Inspector) => void;
  focusRequest?: FocusRequest;
}) {
  const storeKey = layoutKey(fingerprint, graph.key),
    viewKey = viewportKey(fingerprint, graph.key);
  const [nodes, setNodes] = useState<Node<CanvasNodeData>[]>(() =>
      restoredNodes(graph.nodes, storeKey),
    ),
    [edges, setEdges] = useState<Edge[]>(graph.edges);
  const instance = useRef<ReactFlowInstance<Node<CanvasNodeData>, Edge> | null>(
      null,
    ),
    scope = useRef(""),
    lastNodeClick = useRef<{ id: string; at: number } | undefined>(undefined);
  const persist = useCallback(
    (next: Node<CanvasNodeData>[]) => {
      if (typeof window !== "undefined")
        window.localStorage.setItem(
          storeKey,
          JSON.stringify(
            Object.fromEntries(next.map((node) => [node.id, node.position])),
          ),
        );
    },
    [storeKey],
  );
  const restoreViewport = useCallback(
    (fitIfMissing: boolean) => {
      const current = instance.current;
      if (!current || typeof window === "undefined") return;
      try {
        const saved = JSON.parse(
          window.localStorage.getItem(viewKey) || "null",
        );
        if (saved) {
          current.setViewport(saved, { duration: 0 });
          return;
        }
      } catch {
        /* personal viewport is optional */
      }
      if (fitIfMissing) {
        const nodes = graph.initialFitNodeIDs
          ?.map((id) => current.getNode(id))
          .filter(Boolean) as Node<CanvasNodeData>[] | undefined;
        current.fitView({ nodes, padding: 0.16, duration: 0, maxZoom: 1 });
      }
    },
    [graph.initialFitNodeIDs, viewKey],
  );
  useEffect(() => {
    const changedScope = scope.current !== storeKey;
    scope.current = storeKey;
    setNodes((current) =>
      changedScope
        ? restoredNodes(graph.nodes, storeKey)
        : graph.nodes.map((next) => {
            const existing = current.find((item) => item.id === next.id),
              saved = readPositions(storeKey)[next.id];
            return existing
              ? {
                  ...next,
                  position: existing.position,
                  selected: existing.selected,
                }
              : saved
                ? { ...next, position: saved }
                : next;
          }),
    );
    setEdges(graph.edges);
    if (changedScope) requestAnimationFrame(() => restoreViewport(true));
  }, [graph.signature, graph.nodes, graph.edges, restoreViewport, storeKey]);
  useEffect(() => {
    if (!focusRequest || !instance.current) return;
    const node = instance.current.getNode(focusRequest.id);
    if (node)
      instance.current.fitView({
        nodes: [node],
        duration: 220,
        padding: 0.65,
        maxZoom: 1.15,
      });
  }, [focusRequest?.nonce]);
  const onNodesChange = useCallback(
    (changes: NodeChange<Node<CanvasNodeData>>[]) => {
      setNodes((current) => {
        const next = applyNodeChanges(changes, current);
        if (
          changes.some(
            (change) => change.type === "position" && change.dragging === false,
          )
        )
          persist(next);
        return next;
      });
    },
    [persist],
  );
  const settleNode = useCallback(
    (_: unknown, node: Node<CanvasNodeData>) =>
      setNodes((current) => {
        const resolved = resolveOverlap(node, current),
          next = current.map((item) =>
            item.id === resolved.id
              ? { ...item, position: resolved.position }
              : item,
          );
        persist(next);
        return next;
      }),
    [persist],
  );
  const fit = useCallback(
    () =>
      instance.current?.fitView({ padding: 0.16, duration: 200, maxZoom: 1 }),
    [],
  );
  const autoLayout = useCallback(() => {
    const next = graph.nodes;
    setNodes(next);
    persist(next);
    requestAnimationFrame(fit);
  }, [fit, graph.nodes, persist]);
  const resetLayout = useCallback(() => {
    if (typeof window !== "undefined") window.localStorage.removeItem(storeKey);
    const next = graph.nodes;
    setNodes(next);
    persist(next);
    requestAnimationFrame(fit);
  }, [fit, graph.nodes, persist, storeKey]);
  return (
    <div
      className="architecture-v2-canvas"
      data-testid="architecture-canvas"
      aria-label="Interactive architecture canvas"
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onNodeDragStop={settleNode}
        onNodeClick={(_, node) => {
          setNodes((current) =>
            current.map((item) => ({ ...item, selected: item.id === node.id })),
          );
          setEdges((current) =>
            current.map((edge) => ({
              ...edge,
              selected: edge.source === node.id || edge.target === node.id,
            })),
          );
          const value = node.data.value || { kind: "flow", value: node.data };
          onInspector(value);
          const now = Date.now(),
            previous = lastNodeClick.current;
          if (previous?.id === node.id && now - previous.at < 420) {
            lastNodeClick.current = undefined;
            if (value.kind === "group" || value.kind === "unlinked")
              onToggleGroup(value);
            else onDrillDown(value);
          } else lastNodeClick.current = { id: node.id, at: now };
        }}
        onEdgeClick={(_, edge) => {
          setEdges((current) =>
            current.map((item) => ({ ...item, selected: item.id === edge.id })),
          );
          const data = edge.data as any;
          onInspector({
            kind: "relation",
            value: data?.relation || data?.relations?.[0],
          });
        }}
        onPaneClick={() => {
          setNodes((current) =>
            current.map((node) => ({ ...node, selected: false })),
          );
          setEdges((current) =>
            current.map((edge) => ({ ...edge, selected: false })),
          );
          onClearInspector();
        }}
        onInit={(value) => {
          instance.current = value;
          restoreViewport(true);
        }}
        onMoveEnd={(_, viewport) => {
          if (typeof window !== "undefined")
            window.localStorage.setItem(viewKey, JSON.stringify(viewport));
        }}
        minZoom={0.15}
        maxZoom={1.8}
        nodesConnectable={false}
        deleteKeyCode={null}
        defaultEdgeOptions={{ type: "orthogonal" }}
      >
        <Background gap={22} size={1} />
        <Controls showInteractive={false} />
        <MiniMap
          pannable
          zoomable
          nodeColor={(node) =>
            node.data?.kind === "service" ? "#236449" : "#98a49c"
          }
        />
      </ReactFlow>
      <div className="architecture-v2-canvas-actions">
        <button className="button" onClick={fit}>
          Уместить
        </button>
        <button className="button" onClick={autoLayout}>
          Авторасстановка
        </button>
        <button className="button" onClick={resetLayout}>
          Сбросить раскладку
        </button>
      </div>
      <div className="architecture-v2-canvas-note">
        Раскладка и viewport личные для браузера и CURRENT fingerprint;
        перетаскивание не меняет архитектуру.
      </div>
    </div>
  );
}

function serviceNode(
  service: V2Service,
  position: { x: number; y: number },
  isolated = false,
  dimmed = false,
): Node<CanvasNodeData> {
  const purpose = statement(service.manifest?.purpose);
  return {
    id: serviceID(service),
    type: "architecture",
    position,
    data: {
      kind: "service",
      title: serviceName(service),
      subtitle: isolated ? "No confirmed CURRENT relations" : purpose,
      dimmed,
      value: { kind: "service", value: service },
    },
  };
}
function stronglyConnected(ids: string[], relations: V2Relation[]) {
  const graph = new Map(ids.map((id) => [id, [] as string[]]));
  relations.forEach((relation) =>
    graph.get(relation.source_project_id)?.push(relation.target_project_id),
  );
  const index = new Map<string, number>(),
    low = new Map<string, number>(),
    stack: string[] = [],
    inStack = new Set<string>(),
    result: string[][] = [];
  let next = 0;
  const visit = (id: string) => {
    index.set(id, next);
    low.set(id, next);
    next += 1;
    stack.push(id);
    inStack.add(id);
    for (const target of graph.get(id) || []) {
      if (!index.has(target)) {
        visit(target);
        low.set(id, Math.min(low.get(id)!, low.get(target)!));
      } else if (inStack.has(target))
        low.set(id, Math.min(low.get(id)!, index.get(target)!));
    }
    if (low.get(id) === index.get(id)) {
      const component: string[] = [];
      let target = "";
      do {
        target = stack.pop()!;
        inStack.delete(target);
        component.push(target);
      } while (target !== id);
      result.push(component.sort());
    }
  };
  ids
    .slice()
    .sort()
    .forEach((id) => {
      if (!index.has(id)) visit(id);
    });
  return result;
}
function connectedComponents(ids: string[], relations: V2Relation[]) {
  const neighbours = new Map(ids.map((id) => [id, new Set<string>()]));
  relations.forEach((relation) => {
    neighbours.get(relation.source_project_id)?.add(relation.target_project_id);
    neighbours.get(relation.target_project_id)?.add(relation.source_project_id);
  });
  const seen = new Set<string>(),
    result: string[][] = [];
  ids
    .slice()
    .sort()
    .forEach((id) => {
      if (seen.has(id)) return;
      const queue = [id],
        component: string[] = [];
      seen.add(id);
      while (queue.length) {
        const item = queue.shift()!;
        component.push(item);
        neighbours.get(item)?.forEach((target) => {
          if (!seen.has(target)) {
            seen.add(target);
            queue.push(target);
          }
        });
      }
      result.push(component.sort());
    });
  return result.sort((a, b) => b.length - a.length || a[0].localeCompare(b[0]));
}
function relationAwareLayout(services: V2Service[], relations: V2Relation[]) {
  const positions = new Map<string, { x: number; y: number }>();
  let offsetX = 0,
    offsetY = 0,
    rowHeight = 0;
  const rowLimit = 2200;
  connectedComponents(services.map(serviceID), relations).forEach(
    (component) => {
      const inside = relations.filter(
        (relation) =>
          component.includes(relation.source_project_id) &&
          component.includes(relation.target_project_id),
      );
      const sccs = stronglyConnected(component, inside);
      const owner = new Map<string, number>();
      sccs.forEach((items, index) =>
        items.forEach((id) => owner.set(id, index)),
      );
      const outgoing = new Map<number, Set<number>>(
        sccs.map((_, index) => [index, new Set<number>()]),
      );
      const indegree = new Map<number, number>(
        sccs.map((_, index) => [index, 0]),
      );
      inside.forEach((relation) => {
        const from = owner.get(relation.source_project_id)!,
          to = owner.get(relation.target_project_id)!;
        if (from !== to && !outgoing.get(from)!.has(to)) {
          outgoing.get(from)!.add(to);
          indegree.set(to, indegree.get(to)! + 1);
        }
      });
      const queue = [...indegree.entries()]
        .filter(([, value]) => value === 0)
        .map(([key]) => key)
        .sort((a, b) => sccs[a][0].localeCompare(sccs[b][0]));
      const rank = new Map<number, number>(sccs.map((_, index) => [index, 0]));
      while (queue.length) {
        const item = queue.shift()!;
        [...outgoing.get(item)!]
          .sort((a, b) => sccs[a][0].localeCompare(sccs[b][0]))
          .forEach((target) => {
            rank.set(target, Math.max(rank.get(target)!, rank.get(item)! + 1));
            indegree.set(target, indegree.get(target)! - 1);
            if (indegree.get(target) === 0) queue.push(target);
          });
      }
      const layers = new Map<number, string[]>();
      sccs.forEach((items, index) => {
        const layer = rank.get(index)!;
        layers.set(layer, [...(layers.get(layer) || []), ...items]);
      });
      const maxRows = Math.max(
        ...[...layers.values()].map((items) => items.length),
      );
      const maxLayer = Math.max(...layers.keys());
      const componentWidth = (maxLayer + 1) * (NODE_WIDTH + 122) - 122;
      const componentHeight = maxRows * (NODE_HEIGHT + 34) - 34;
      if (offsetX > 0 && offsetX + componentWidth > rowLimit) {
        offsetX = 0;
        offsetY += rowHeight + 132;
        rowHeight = 0;
      }
      [...layers.entries()]
        .sort(([a], [b]) => a - b)
        .forEach(([layer, items]) =>
          items.sort().forEach((id, row) =>
            positions.set(id, {
              x: offsetX + layer * (NODE_WIDTH + 122),
              y: offsetY + row * (NODE_HEIGHT + 34),
            }),
          ),
        );
      offsetX += componentWidth + 122;
      rowHeight = Math.max(rowHeight, componentHeight);
    },
  );
  return positions;
}

function platformGraph(
  catalog: V2Catalog,
  mode: V2Mode,
  selected?: string,
  focus?: string,
  unlinkedExpanded = false,
): CanvasGraph {
  const all = catalog.platform?.services || [];
  const visible = visibleServices(all, mode);
  const visibleIDs = new Set(visible.map(serviceID));
  const resolved = (catalog.platform?.relations || []).filter(
    (relation) =>
      visibleIDs.has(relation.source_project_id) &&
      visibleIDs.has(relation.target_project_id) &&
      relation.source_project_id !== relation.target_project_id,
  );
  const degree = new Map<string, number>();
  resolved.forEach((relation) => {
    degree.set(
      relation.source_project_id,
      (degree.get(relation.source_project_id) || 0) + 1,
    );
    degree.set(
      relation.target_project_id,
      (degree.get(relation.target_project_id) || 0) + 1,
    );
  });
  const connected = visible.filter((service) => degree.has(serviceID(service)));
  const unlinked = visible.filter((service) => !degree.has(serviceID(service)));
  const positions = relationAwareLayout(connected, resolved);
  const nearby = selected
    ? new Set([
        selected,
        ...resolved.flatMap((relation) =>
          relation.source_project_id === selected
            ? [relation.target_project_id]
            : relation.target_project_id === selected
              ? [relation.source_project_id]
              : [],
        ),
      ])
    : undefined;
  const focusIDs = focus
    ? new Set([
        focus,
        ...resolved.flatMap((relation) =>
          relation.source_project_id === focus
            ? [relation.target_project_id]
            : relation.target_project_id === focus
              ? [relation.source_project_id]
              : [],
        ),
      ])
    : undefined;
  const connectedBottom = Math.max(
    0,
    ...[...positions.values()].map((position) => position.y + NODE_HEIGHT),
  );
  const unlinkedOrigin = { x: 0, y: connectedBottom + 220 };
  const unlinkedPositions = new Map(
    unlinked.map((service, index) => [
      serviceID(service),
      {
        x: unlinkedOrigin.x + (index % 4) * (NODE_WIDTH + 34),
        y: unlinkedOrigin.y + 144 + Math.floor(index / 4) * (NODE_HEIGHT + 28),
      },
    ]),
  );
  const shownConnected = focusIDs
    ? connected.filter((service) => focusIDs.has(serviceID(service)))
    : connected;
  const shownUnlinked = focusIDs
    ? unlinked.filter((service) => focusIDs.has(serviceID(service)))
    : unlinkedExpanded
      ? unlinked
      : [];
  const nodes: Node<CanvasNodeData>[] = [
    ...shownConnected.map((service) =>
      serviceNode(
        service,
        positions.get(serviceID(service)) || { x: 0, y: 0 },
        false,
        Boolean(nearby && !nearby.has(serviceID(service))),
      ),
    ),
    ...shownUnlinked.map((service) =>
      serviceNode(
        service,
        unlinkedPositions.get(serviceID(service)) || unlinkedOrigin,
        true,
        Boolean(nearby && !nearby.has(serviceID(service))),
      ),
    ),
  ];
  // Component headings are visual regions, not architecture entities. They
  // make independently connected areas legible without inventing a domain.
  connectedComponents(connected.map(serviceID), resolved).forEach(
    (component, index) => {
      const members = component
        .map((id) => positions.get(id))
        .filter(Boolean) as { x: number; y: number }[];
      if (!members.length) return;
      nodes.push({
        id: `component:${index}`,
        type: "architecture",
        position: {
          x: Math.min(...members.map((position) => position.x)),
          y: Math.max(
            0,
            Math.min(...members.map((position) => position.y)) - 126,
          ),
        },
        data: {
          kind: "annotation",
          title: `Connected architecture · ${component.length} services`,
          subtitle: "Confirmed CURRENT relations",
          value: {
            kind: "flow",
            value: {
              title: "Connected architecture",
              description:
                "This region is formed only by confirmed CURRENT relations.",
            },
          },
        },
      });
    },
  );
  if (!focusIDs && unlinked.length) {
    nodes.push({
      id: "platform:unlinked",
      type: "architecture",
      position: unlinkedOrigin,
      data: {
        kind: "group",
        title: `Unlinked / no confirmed relations (${unlinked.length})`,
        subtitle: unlinkedExpanded
          ? "Expanded · double-click to collapse"
          : "Collapsed · double-click to expand",
        value: {
          kind: "unlinked",
          value: { services: unlinked, expanded: unlinkedExpanded },
        },
      },
    });
  }
  const grouped = new Map<string, V2Relation[]>();
  resolved
    .filter(
      (relation) =>
        !focusIDs ||
        (focusIDs.has(relation.source_project_id) &&
          focusIDs.has(relation.target_project_id)),
    )
    .forEach((relation) => {
      const key = `${relation.source_project_id}:${relation.target_project_id}:${relation.transport || "relation"}`;
      grouped.set(key, [...(grouped.get(key) || []), relation]);
    });
  const edgeEntries = [...grouped.entries()];
  const pairTotals = new Map<string, number>();
  edgeEntries.forEach(([, relations]) => {
    const relation = relations[0];
    const pair = `${relation.source_project_id}:${relation.target_project_id}`;
    pairTotals.set(pair, (pairTotals.get(pair) || 0) + 1);
  });
  const pairIndex = new Map<string, number>();
  const edges = edgeEntries.map(([id, relations]) => {
    const relation = relations[0],
      kind = edgeKind(relation);
    const pair = `${relation.source_project_id}:${relation.target_project_id}`;
    const index = pairIndex.get(pair) || 0;
    pairIndex.set(pair, index + 1);
    return {
      ...semanticEdge(
        id,
        relation.source_project_id,
        relation.target_project_id,
        `${String(relation.transport || "relation").toUpperCase()}${relations.length > 1 ? ` · ${relations.length} documented relations` : ""}`,
        kind,
        {
          ...relation,
          relation_count: relations.length,
          related_relations: relations,
        },
        false,
        index,
        pairTotals.get(pair) || 1,
      ),
      style: edgeStyle(
        kind,
        Boolean(
          nearby &&
          !nearby.has(relation.source_project_id) &&
          !nearby.has(relation.target_project_id),
        ),
      ),
    };
  });
  return {
    key: "platform",
    signature: `platform:${mode}:${selected || ""}:${focus || ""}:${unlinkedExpanded}:${nodes.map((node) => node.id).join(",")}:${edges.map((edge) => edge.id).join(",")}`,
    nodes,
    edges,
    initialFitNodeIDs: focusIDs
      ? [...focusIDs]
      : [...degree.keys(), ...(unlinked.length ? ["platform:unlinked"] : [])],
  };
}
function serviceGraph(
  catalog: V2Catalog,
  service: V2Service,
  groupBy: "capability" | "transport",
  collapsedGroups = new Set<string>(),
): CanvasGraph {
  const nodes: Node<CanvasNodeData>[] = [
      {
        id: "service",
        type: "architecture",
        position: { x: 390, y: 230 },
        data: {
          kind: "service",
          title: serviceName(service),
          subtitle: statement(service.manifest?.purpose),
          value: { kind: "service", value: service },
        },
      },
    ],
    edges: Edge[] = [];
  list<any>(service.manifest?.owned_resources)
    .slice(0, 8)
    .forEach((resource, index) => {
      const id = `resource:${index}`;
      nodes.push({
        id,
        type: "architecture",
        position: { x: 0, y: 36 + index * 128 },
        data: {
          kind: "resource",
          title: resource.name || "unknown resource",
          subtitle: `${resource.type || "resource"} · ${statement(resource.description)}`,
          value: {
            kind: "flow",
            value: {
              title: resource.name,
              description: statement(resource.description),
              evidence: resource.description?.evidence,
              confidence: resource.description?.confidence,
            },
          },
        },
      });
      edges.push(
        semanticEdge(`owns:${id}`, "service", id, "owns", "owns", {
          description: resource.description,
          evidence: resource.description?.evidence,
          confidence: resource.description?.confidence,
          external_target: resource.name,
        }),
      );
    });
  const groups = groupOperations(service, groupBy);
  let groupY = 28;
  groups.forEach((group) => {
    const id = `group:${group.name}`;
    const collapsed = collapsedGroups.has(group.name);
    nodes.push({
      id,
      type: "architecture",
      position: { x: 790, y: groupY },
      data: {
        kind: "group",
        title: group.name,
        subtitle: `${group.operations.length} operations · double-click to ${collapsed ? "expand" : "collapse"}`,
        value: { kind: "group", value: { ...group, collapsed } },
      },
    });
    edges.push(
      semanticEdge(
        `contains:${id}`,
        "service",
        id,
        groupBy === "transport" ? "transport" : "capability",
        "contains",
      ),
    );
    if (!collapsed)
      group.operations.forEach((operation, operationIndex) => {
        const operationNodeID = `operation:${operationID(operation)}`;
        nodes.push({
          id: operationNodeID,
          type: "architecture",
          position: { x: 1160, y: groupY + operationIndex * 116 },
          data: {
            kind: "operation",
            title: operationIdentity(operation),
            subtitle: statement(operation.manifest?.business_task),
            value: { kind: "operation", value: operation },
          },
        });
        edges.push(
          semanticEdge(
            `${id}:${operationNodeID}`,
            id,
            operationNodeID,
            "contains",
            "contains",
          ),
        );
      });
    groupY += Math.max(
      NODE_HEIGHT + 48,
      (collapsed ? 1 : group.operations.length) * 116 + 36,
    );
  });
  const known = new Map(
      (catalog.platform?.services || []).map((item) => [serviceID(item), item]),
    ),
    relations = (catalog.platform?.relations || []).filter(
      (relation) =>
        (relation.source_project_id === serviceID(service) ||
          relation.target_project_id === serviceID(service)) &&
        relation.source_project_id &&
        relation.target_project_id &&
        known.has(relation.source_project_id) &&
        known.has(relation.target_project_id),
    ),
    counterpartIDs = [
      ...new Set(
        relations
          .flatMap((relation) => [
            relation.source_project_id,
            relation.target_project_id,
          ])
          .filter((id) => id !== serviceID(service)),
      ),
    ].slice(0, 8);
  counterpartIDs.forEach((id, index) => {
    const counterpart = known.get(id)!,
      nodeID = `counterpart:${id}`;
    nodes.push({
      id: nodeID,
      type: "architecture",
      position: {
        x: 390 + (index % 2) * 330,
        y: Math.max(groupY + 48, 570) + Math.floor(index / 2) * 128,
      },
      data: {
        kind: "external",
        title: serviceName(counterpart),
        subtitle: "confirmed internal service",
        value: { kind: "service", value: counterpart },
      },
    });
  });
  relations.forEach((relation, index) => {
    const source =
        relation.source_project_id === serviceID(service)
          ? "service"
          : `counterpart:${relation.source_project_id}`,
      target =
        relation.target_project_id === serviceID(service)
          ? "service"
          : `counterpart:${relation.target_project_id}`;
    if (
      nodes.some((node) => node.id === source) &&
      nodes.some((node) => node.id === target)
    )
      edges.push(
        semanticEdge(
          `relation:${index}`,
          source,
          target,
          relationLabel(relation),
          edgeKind(relation),
          relation,
        ),
      );
  });
  return {
    key: `service:${serviceID(service)}`,
    signature: `service:${serviceID(service)}:${groupBy}:${[...collapsedGroups].sort().join(",")}:${nodes.map((node) => node.id).join(",")}`,
    nodes,
    edges,
  };
}
function operationGraph(
  service: V2Service,
  operation: V2Operation,
): CanvasGraph {
  const manifest = operation.manifest || {},
    nodes: Node<CanvasNodeData>[] = [],
    edges: Edge[] = [],
    add = (
      id: string,
      kind: string,
      title: string,
      subtitle: string,
      value: any,
      position: { x: number; y: number },
    ) =>
      nodes.push({
        id,
        type: "architecture",
        position,
        data: { kind, title, subtitle, value: { kind: "flow", value } },
      });
  add(
    "start",
    "process",
    "Request received",
    transport(operation),
    {
      title: operationIdentity(operation),
      description: statement(manifest.trigger?.description),
      evidence: manifest.trigger?.description?.evidence,
      confidence: manifest.trigger?.description?.confidence,
    },
    { x: 0, y: 250 },
  );
  let previous = "start",
    sequenceIndex = 1,
    inputID = "start";
  if (manifest.input?.body) {
    add(
      "input",
      "input",
      manifest.input.body.name || "Request body",
      statement(manifest.input.body.description),
      {
        title: manifest.input.body.name || "Input",
        description: statement(manifest.input.body.description),
        evidence: manifest.input.body.description?.evidence,
        confidence: manifest.input.body.description?.confidence,
      },
      { x: 330, y: 250 },
    );
    edges.push(semanticEdge("start:input", "start", "input", "input", "http"));
    previous = "input";
    inputID = "input";
    sequenceIndex = 2;
  }
  const steps = list<any>(manifest.business_process),
    stepIDs = new Map<string, string>();
  steps.forEach((step, index) => {
    const id = `process:${index}`;
    stepIDs.set(String(step.id || "").toLowerCase(), id);
    add(
      id,
      "process",
      step.id || `step ${index + 1}`,
      statement(step.description),
      {
        title: step.id || "Process step",
        description: statement(step.description),
        evidence: step.description?.evidence,
        confidence: step.description?.confidence,
      },
      { x: sequenceIndex * 330, y: 250 },
    );
    edges.push(
      semanticEdge(
        `${previous}:${id}`,
        previous,
        id,
        "documented step",
        "http",
      ),
    );
    previous = id;
    sequenceIndex += 1;
  });
  if (list(manifest.business_rules).length)
    add(
      "rules",
      "annotation",
      `Rules of operation · ${list(manifest.business_rules).length}`,
      "Evidence-backed reference; not an execution step",
      {
        title: "Rules of operation",
        description:
          "Known rules are aggregate evidence, not an automatically executed flow step.",
        rules: list<any>(manifest.business_rules),
        evidence: list<any>(manifest.business_rules).flatMap((item) =>
          list(item.evidence),
        ),
        confidence: manifest.confidence,
      },
      { x: 660, y: 42 },
    );
  const prepare = stepIDs.get("prepare"),
    validate = stepIDs.get("validate");
  list<any>(manifest.data_access).forEach((item, index) => {
    const id = `data:${index}`;
    add(
      id,
      "resource",
      item.resource || "data",
      `${item.access || "access"} · ${statement(item.description)}`,
      {
        title: item.resource || "Data access",
        description: statement(item.description),
        evidence: item.description?.evidence,
        confidence: item.description?.confidence,
      },
      { x: 690 + index * 320, y: 500 },
    );
    if (prepare)
      edges.push(
        semanticEdge(
          `${prepare}:${id}`,
          prepare,
          id,
          "documented data access",
          "data",
        ),
      );
  });
  list<any>(manifest.external_interactions)
    .filter((item) => !/^unknown\b/i.test(String(item.target || "")))
    .forEach((item, index) => {
      const id = `external:${index}`;
      add(
        id,
        "external",
        item.target || "external interaction",
        `${item.transport || "external"} · ${statement(item.description)}`,
        {
          title: item.target || "External interaction",
          description: statement(item.description),
          evidence: item.description?.evidence,
          confidence: item.description?.confidence,
        },
        { x: 690 + index * 320, y: 630 },
      );
      if (validate)
        edges.push(
          semanticEdge(
            `${validate}:${id}`,
            validate,
            id,
            "documented interaction",
            "external",
          ),
        );
    });
  list<any>(manifest.output?.responses).forEach((item, index) => {
    const id = `output:${index}`,
      status = String(item.status_code || "result"),
      source =
        status === "400" ? inputID : status === "405" ? "start" : previous;
    add(
      id,
      "output",
      `${status} response`,
      statement(item.description),
      {
        title: `${status} response`,
        description: statement(item.description),
        evidence: item.description?.evidence,
        confidence: item.description?.confidence,
      },
      {
        x: (sequenceIndex + index) * 330,
        y: 250 + (status === "405" ? -150 : status === "400" ? 150 : 0),
      },
    );
    edges.push(
      semanticEdge(
        `${source}:${id}`,
        source,
        id,
        "documented outcome",
        "outcome",
      ),
    );
  });
  if (list(manifest.errors).length)
    add(
      "errors",
      "annotation",
      `Documented domain errors · ${list(manifest.errors).length}`,
      "Outcomes with no proven branch-to-step mapping",
      {
        title: "Documented domain errors",
        description:
          "The manifest documents these outcomes but does not prove one branch per displayed flow step.",
        errors: list<any>(manifest.errors),
        evidence: list<any>(manifest.errors).flatMap((item) =>
          list(item.description?.evidence),
        ),
        confidence: manifest.confidence,
      },
      { x: 0, y: 520 },
    );
  return {
    key: `operation:${serviceID(service)}:${operationID(operation)}`,
    signature: `operation:${serviceID(service)}:${operationID(operation)}`,
    nodes,
    edges,
    initialFitNodeIDs: [
      "start",
      inputID,
      ...steps.map((_, index) => `process:${index}`),
    ],
  };
}

function InspectorPanel({
  inspector,
  catalog,
  onOpenService,
  onOpenOperation,
  onToggleGroup,
  onSelectRelation,
  onFocus,
  onPropose,
  level,
  selectedHidden,
}: {
  inspector: Inspector;
  catalog: V2Catalog;
  onOpenService: (service: V2Service) => void;
  onOpenOperation: (operation: V2Operation) => void;
  onToggleGroup: (name: string) => void;
  onSelectRelation: (relation: V2Relation) => void;
  onFocus: (id: string) => void;
  onPropose: () => void;
  level: string;
  selectedHidden: boolean;
}) {
  const content = (() => {
    if (inspector.kind === "service") {
      const service = inspector.value as V2Service,
        manifest = service.manifest || {};
      return (
        <>
          <p className="eyebrow">Selected service</p>
          <h2>{serviceName(service)}</h2>
          <p>{statement(manifest.purpose)}</p>
          {selectedHidden && (
            <p className="warning">
              This selected service is hidden by “Hide technical services”. It
              remains selected in the inspector.
            </p>
          )}
          <dl>
            <dt>Responsibilities</dt>
            <dd>
              {list<any>(manifest.responsibilities).map(statement).join("; ") ||
                "unknown"}
            </dd>
            <dt>Capabilities</dt>
            <dd>
              {list<any>(manifest.capabilities).map(statement).join("; ") ||
                "unknown"}
            </dd>
            <dt>Classification</dt>
            <dd>{classification(service)}</dd>
          </dl>
          {level !== "Service" && (
            <button className="primary" onClick={() => onOpenService(service)}>
              Open service canvas
            </button>
          )}
          {level === "Platform" && (
            <button
              className="button"
              onClick={() => onFocus(serviceID(service))}
            >
              Show on canvas
            </button>
          )}
          <button className="button" onClick={onPropose}>
            Propose a change in TARGET
          </button>
        </>
      );
    }
    if (inspector.kind === "relation") {
      const relation = inspector.value || {},
        services = catalog.platform?.services || [],
        name = (id: string) =>
          serviceName(
            services.find((service) => serviceID(service) === id) ||
              ({ source: { project_name: id } } as V2Service),
          ),
        count = Number(relation.relation_count || 1);
      return (
        <>
          <p className="eyebrow">Confirmed relation</p>
          <h2>{relationLabel(relation) || "Relation"}</h2>
          <p>{statement(relation.description)}</p>
          <dl>
            <dt>From</dt>
            <dd>{name(relation.source_project_id)}</dd>
            <dt>To</dt>
            <dd>
              {relation.target_project_id
                ? name(relation.target_project_id)
                : relation.external_target || "unknown"}
            </dd>
            <dt>What “· {count}” counts</dt>
            <dd>
              {count} documented relation{count === 1 ? "" : "s"} with the same
              source, target and transport.
            </dd>
            <dt>Resolution</dt>
            <dd>{relationResolution(relation, services)}</dd>
            <dt>Direction</dt>
            <dd>{relation.direction || "unknown"}</dd>
            <dt>Confidence</dt>
            <dd>{confidence(relation.confidence)}</dd>
            <dt>Evidence</dt>
            <dd>{evidence(relation.evidence)}</dd>
          </dl>
        </>
      );
    }
    if (inspector.kind === "unlinked") {
      const value = inspector.value || {};
      return (
        <>
          <p className="eyebrow">CURRENT relation status</p>
          <h2>
            Unlinked / no confirmed relations (
            {list<any>(value.services).length})
          </h2>
          <p>
            These services have no confirmed internal CURRENT relation in the
            catalog. This is not a claim that no integration exists outside the
            available evidence.
          </p>
          <button
            className="button"
            onClick={() => onToggleGroup("__unlinked__")}
          >
            {value.expanded
              ? "Collapse unlinked services"
              : "Expand unlinked services"}
          </button>
        </>
      );
    }
    if (inspector.kind === "group") {
      const group = inspector.value;
      return (
        <>
          <p className="eyebrow">Operation group</p>
          <h2>{group.name}</h2>
          <p>
            {group.operations.length} operations. The group is{" "}
            {group.collapsed ? "collapsed" : "expanded"}; double-click it on the
            canvas to toggle without moving the camera.
          </p>
          <button className="button" onClick={() => onToggleGroup(group.name)}>
            {group.collapsed ? "Expand this group" : "Collapse this group"}
          </button>
        </>
      );
    }
    if (inspector.kind === "operation") {
      const operation = inspector.value as V2Operation,
        manifest = operation.manifest || {};
      return (
        <>
          <p className="eyebrow">Selected operation</p>
          <h2>{operationIdentity(operation)}</h2>
          <p>{statement(manifest.business_task)}</p>
          <dl>
            <dt>When / why</dt>
            <dd>{statement(manifest.trigger?.description)}</dd>
            <dt>Main rules</dt>
            <dd>
              {list<any>(manifest.business_rules).map(statement).join("; ") ||
                "unknown"}
            </dd>
            <dt>Result</dt>
            <dd>
              {list<any>(manifest.output?.responses)
                .map(
                  (item) =>
                    `${item.status_code}: ${statement(item.description)}`,
                )
                .join("; ") || "unknown"}
            </dd>
            <dt>Failure meaning</dt>
            <dd>
              {list<any>(manifest.errors)
                .map((item) => item.code || statement(item))
                .join("; ") || "unknown"}
            </dd>
            <dt>Evidence</dt>
            <dd>{evidence(manifest.evidence)}</dd>
          </dl>
          {level !== "Operation" && (
            <button
              className="primary"
              onClick={() => onOpenOperation(operation)}
            >
              Open operation canvas
            </button>
          )}
          <button className="button" onClick={onPropose}>
            Propose a change in TARGET
          </button>
        </>
      );
    }
    if (inspector.kind === "flow") {
      const item = inspector.value || {};
      return (
        <>
          <p className="eyebrow">Selected element</p>
          <h2>{item.title || "Architecture element"}</h2>
          <p>{item.description || "unknown"}</p>
          {Array.isArray(item.rules) && (
            <>
              <h3>Rules</h3>
              <ul className="architecture-v2-detail-list">
                {item.rules.map((rule: any, index: number) => (
                  <li key={index}>{statement(rule)}</li>
                ))}
              </ul>
            </>
          )}
          {Array.isArray(item.errors) && (
            <>
              <h3>Documented outcomes</h3>
              <ul className="architecture-v2-detail-list">
                {item.errors.map((error: any, index: number) => (
                  <li key={index}>
                    <strong>{error.code || "unknown"}</strong>:{" "}
                    {statement(error.description)}
                  </li>
                ))}
              </ul>
            </>
          )}
          <dl>
            <dt>Confidence</dt>
            <dd>{confidence(item.confidence)}</dd>
            <dt>Evidence</dt>
            <dd>{evidence(item.evidence)}</dd>
          </dl>
        </>
      );
    }
    const services = catalog.platform?.services || [],
      relations = (catalog.platform?.relations || [])
        .filter(
          (relation) =>
            relation.source_project_id &&
            relation.target_project_id &&
            relation.source_project_id !== relation.target_project_id,
        )
        .slice(0, 6),
      name = (id: string) =>
        serviceName(
          services.find((service) => serviceID(service) === id) ||
            ({ source: { project_name: id } } as V2Service),
        );
    return (
      <>
        <p className="eyebrow">CURRENT canvas</p>
        <h2>Select a node or connection</h2>
        <p>
          Only confirmed internal relations are drawn. Unresolved and unknown
          references stay in the inspector; they do not become invented nodes.
        </p>
        <details open>
          <summary>Confirmed connections</summary>
          <div className="architecture-v2-relation-list">
            {relations.map((relation) => (
              <button
                key={relation.id}
                className="button"
                onClick={() => onSelectRelation(relation)}
              >
                {name(relation.source_project_id)} →{" "}
                {name(relation.target_project_id)}
                <small>{relationLabel(relation)}</small>
              </button>
            ))}
          </div>
        </details>
        <button className="primary" onClick={onPropose}>
          Propose a change in TARGET
        </button>
      </>
    );
  })();
  return (
    <aside
      className="architecture-v2-inspector"
      aria-label="Architecture element inspector"
    >
      <div className="architecture-v2-inspector-scroll">
        {content}
        <details className="architecture-v2-advanced">
          <summary>Advanced</summary>
          <p>
            UUIDs, raw JSON and Mermaid source remain outside the normal
            exploration flow.
          </p>
          <code>
            {JSON.stringify(inspector.value || { mode: "CURRENT" }, null, 2)}
          </code>
        </details>
      </div>
    </aside>
  );
}
function Toolbar({
  mode,
  onMode,
  search,
  setSearch,
  matches,
  onSelectService,
  onReference,
  onPropose,
  onBack,
  level,
  service,
  operation,
  focus,
  clearFocus,
  visibleCount,
  hiddenCount,
}: {
  mode: V2Mode;
  onMode: (mode: V2Mode) => void;
  search: string;
  setSearch: (value: string) => void;
  matches: V2Service[];
  onSelectService: (service: V2Service) => void;
  onReference: () => void;
  onPropose: () => void;
  onBack: () => void;
  level: string;
  service?: V2Service;
  operation?: V2Operation;
  focus?: string;
  clearFocus: () => void;
  visibleCount: number;
  hiddenCount: number;
}) {
  const crumbs = [
    "Platform",
    service && serviceName(service),
    operation && operationIdentity(operation),
  ]
    .filter(Boolean)
    .join(" / ");
  return (
    <header className="architecture-v2-toolbar">
      <div>
        <p className="eyebrow">CURRENT · UX preview</p>
        <h1>{crumbs}</h1>
        <p>Read-only architecture exploration. Canvas layout is local only.</p>
      </div>
      <div className="architecture-v2-tools">
        {level === "Platform" && (
          <>
            <div className="segmented" aria-label="Platform service filter">
              <button
                className={mode === "all" ? "selected" : ""}
                onClick={() => onMode("all")}
              >
                All services
              </button>
              <button
                className={mode === "hideTechnical" ? "selected" : ""}
                onClick={() => onMode("hideTechnical")}
              >
                Hide technical
              </button>
            </div>
            <small className="architecture-v2-filter-count">
              {visibleCount} visible
              {hiddenCount ? ` · ${hiddenCount} hidden` : ""}
            </small>
          </>
        )}
        <div className="architecture-v2-search">
          <input
            aria-label="Find service"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Find a service…"
          />
          {search && (
            <div className="architecture-v2-search-results">
              {matches.slice(0, 8).map((item) => (
                <button
                  key={serviceID(item)}
                  onClick={() => onSelectService(item)}
                >
                  {serviceName(item)}
                  <small>{statement(item.manifest?.purpose)}</small>
                </button>
              ))}
              {!matches.length && <span>No matching CURRENT service</span>}
            </div>
          )}
        </div>
        {focus && (
          <button className="button" onClick={clearFocus}>
            Clear focus
          </button>
        )}
        <button className="button" onClick={onReference}>
          Open reference scenario
        </button>
        <button className="primary" onClick={onPropose}>
          Propose a change
        </button>
        {level !== "Platform" && (
          <button className="button" onClick={onBack}>
            Back
          </button>
        )}
      </div>
    </header>
  );
}
export function ArchitectureV2Explorer() {
  const [catalog, setCatalog] = useState<V2Catalog>(),
    [error, setError] = useState(""),
    [mode, setMode] = useState<V2Mode>("all"),
    [selectedService, setSelectedService] = useState<V2Service>(),
    [selectedOperation, setSelectedOperation] = useState<V2Operation>(),
    [platformSelection, setPlatformSelection] = useState<V2Service>(),
    [inspector, setInspector] = useState<Inspector>({ kind: "platform" }),
    [search, setSearch] = useState(""),
    [focus, setFocus] = useState(""),
    [groupBy, setGroupBy] = useState<"capability" | "transport">("transport"),
    [collapsedGroups, setCollapsedGroups] = useState<string[]>([]),
    [unlinkedExpanded, setUnlinkedExpanded] = useState(false),
    [targetDrawerOpen, setTargetDrawerOpen] = useState(false),
    [targetDraft, setTargetDraft] = useState<TargetChangeDraft>(
      targetDrawerContextDraft(),
    ),
    [target, setTarget] = useState<ArchitectureTarget>(),
    [targetBusy, setTargetBusy] = useState<TargetLifecycleAction>(),
    [targetError, setTargetError] = useState(""),
    [targetResult, setTargetResult] = useState<unknown>(),
    [focusRequest, setFocusRequest] = useState<FocusRequest>();
  const currentState = useCallback(
    () => ({ architectureV2: { mode, focus, groupBy, unlinkedExpanded } }),
    [focus, groupBy, mode, unlinkedExpanded],
  );
  const applyRoute = useCallback(
    (value: V2Catalog, state: any = window.history.state) => {
      const saved = state?.architectureV2;
      if (saved?.mode === "all" || saved?.mode === "hideTechnical")
        setMode(saved.mode);
      if (typeof saved?.focus === "string") setFocus(saved.focus);
      if (saved?.groupBy === "capability" || saved?.groupBy === "transport")
        setGroupBy(saved.groupBy);
      if (typeof saved?.unlinkedExpanded === "boolean")
        setUnlinkedExpanded(saved.unlinkedExpanded);
      const parts = window.location.pathname.split("/"),
        servicePart = decodeURIComponent(
          parts[parts.indexOf("services") + 1] || "",
        ),
        operationPart = decodeURIComponent(
          parts[parts.indexOf("operations") + 1] || "",
        ),
        service = (value.platform?.services || []).find(
          (item) =>
            serviceSlug(item) === servicePart ||
            serviceName(item) === servicePart,
        ),
        operation = service
          ? allOperations(service).find(
              (item) =>
                operationSlug(item) === operationPart ||
                operationID(item) === operationPart,
            )
          : undefined;
      setSelectedService(service);
      setSelectedOperation(operation);
      setCollapsedGroups(
        service
          ? readCollapsedGroups(
              String(value.fingerprint || "unknown"),
              serviceID(service),
            )
          : [],
      );
      setPlatformSelection(undefined);
      setInspector(
        operation
          ? { kind: "operation", value: operation }
          : service
            ? { kind: "service", value: service }
            : { kind: "platform" },
      );
    },
    [],
  );
  useEffect(() => {
    readCatalog()
      .then((value) => {
        setCatalog(value);
        applyRoute(value);
      })
      .catch((value) => setError(value.message));
  }, [applyRoute]);
  useEffect(() => {
    if (!catalog) return;
    const pop = () => applyRoute(catalog);
    window.addEventListener("popstate", pop);
    return () => window.removeEventListener("popstate", pop);
  }, [applyRoute, catalog]);
  const requestFocus = useCallback(
      (id: string) =>
        setFocusRequest((current) => ({
          id,
          nonce: (current?.nonce || 0) + 1,
        })),
      [],
    ),
    push = useCallback(
      (path: string) => window.history.pushState(currentState(), "", path),
      [currentState],
    ),
    replaceState = useCallback(
      () =>
        window.history.replaceState(currentState(), "", window.location.href),
      [currentState],
    );
  const navigateService = useCallback(
      (service: V2Service) => {
        push(routeForService(service));
        setSelectedService(service);
        setSelectedOperation(undefined);
        setCollapsedGroups(
          readCollapsedGroups(
            String(catalog?.fingerprint || "unknown"),
            serviceID(service),
          ),
        );
        setPlatformSelection(undefined);
        setInspector({ kind: "service", value: service });
        requestFocus("service");
      },
      [catalog?.fingerprint, push, requestFocus],
    ),
    navigateOperation = useCallback(
      (operation: V2Operation) => {
        if (!selectedService) return;
        push(routeForOperation(selectedService, operation));
        setSelectedOperation(operation);
        setInspector({ kind: "operation", value: operation });
        requestFocus("process:0");
      },
      [push, requestFocus, selectedService],
    ),
    goPlatform = useCallback(() => {
      push("/architecture-v2/current/platform");
      setSelectedService(undefined);
      setSelectedOperation(undefined);
      setPlatformSelection(undefined);
      setInspector({ kind: "platform" });
      setFocus("");
    }, [push]);
  if (error)
    return (
      <div className="page">
        <section className="panel">
          <h1>Architecture V2 unavailable</h1>
          <p className="warning">{error}</p>
        </section>
      </div>
    );
  if (!catalog)
    return (
      <div className="page">
        <section className="panel">
          Loading manifest-backed CURRENT catalog…
        </section>
      </div>
    );
  const services = catalog.platform?.services || [],
    matches = services.filter((service) =>
      searchText(
        `${serviceName(service)} ${statement(service.manifest?.purpose)}`,
      ).includes(searchText(search)),
    ),
    fingerprint = String(catalog.fingerprint || "unknown"),
    level = selectedOperation
      ? "Operation"
      : selectedService
        ? "Service"
        : "Platform",
    selectedID = platformSelection ? serviceID(platformSelection) : undefined,
    graph =
      selectedOperation && selectedService
        ? operationGraph(selectedService, selectedOperation)
        : selectedService
          ? serviceGraph(
              catalog,
              selectedService,
              groupBy,
              new Set(collapsedGroups),
            )
          : platformGraph(catalog, mode, selectedID, focus, unlinkedExpanded),
    visibleCount = visibleServices(services, mode).length,
    selectedHidden = Boolean(
      platformSelection &&
      !visibleServices(services, mode).some(
        (item) => serviceID(item) === serviceID(platformSelection),
      ),
    );
  const onInspector = (value: Inspector) => {
      if (value.kind === "service" && !selectedService)
        setPlatformSelection(value.value);
      setInspector(value);
    },
    drillDown = (value: Inspector) => {
      if (value.kind === "service") navigateService(value.value);
      if (value.kind === "operation") navigateOperation(value.value);
    },
    openReference = () => {
      const service = services.find(
          (item) => serviceName(item) === referenceService,
        ),
        operation =
          service &&
          allOperations(service).find(
            (item) => operationID(item) === referenceOperation,
          );
      if (service && operation) {
        push(routeForOperation(service, operation));
        setSelectedService(service);
        setSelectedOperation(operation);
        setPlatformSelection(undefined);
        setInspector({ kind: "operation", value: operation });
        requestFocus("process:0");
      }
    },
    openTargetProposal = () => {
      const service = selectedService || platformSelection;
      setTargetDraft(
        targetDrawerContextDraft(
          service ? serviceID(service) : undefined,
          selectedOperation ? operationID(selectedOperation) : undefined,
        ),
      );
      setTargetError("");
      setTargetResult(undefined);
      setTargetDrawerOpen(true);
    },
    toggleGroup = (value: Inspector) => {
      if (value.kind === "unlinked") {
        setUnlinkedExpanded((current) => !current);
        setInspector({
          kind: "unlinked",
          value: { ...(value.value || {}), expanded: !value.value?.expanded },
        });
        return;
      }
      const name = String(value.value?.name || "");
      if (!name || !selectedService) return;
      setCollapsedGroups((current) => {
        const next = current.includes(name)
          ? current.filter((item) => item !== name)
          : [...current, name];
        window.localStorage.setItem(
          collapsedGroupsKey(fingerprint, serviceID(selectedService)),
          JSON.stringify(next),
        );
        const group = groupOperations(selectedService, groupBy).find(
          (item) => item.name === name,
        );
        setInspector({
          kind: "group",
          value: { ...group, collapsed: next.includes(name) },
        });
        return next;
      });
    },
    saveTargetDraft = async () => {
      setTargetBusy("save");
      setTargetError("");
      try {
        const changes = [targetDraft];
        const value = target
          ? await apiPost(`/api/v1/architecture/targets/${target.id}/changes`, {
              expected_revision: target.revision,
              current_fingerprint: fingerprint,
              changes,
            })
          : await apiPost("/api/v1/architecture/targets", {
              current_fingerprint: fingerprint,
              idempotency_key:
                globalThis.crypto?.randomUUID?.() || `v2-drawer-${Date.now()}`,
              changes,
            });
        const next = architectureTargetResponseSchema.parse(value);
        setTarget(next);
        setTargetResult(value);
      } catch (value) {
        setTargetError(
          value instanceof Error
            ? value.message
            : "TARGET draft request failed",
        );
      } finally {
        setTargetBusy(undefined);
      }
    },
    submitTarget = async () => {
      setTargetBusy("submit");
      setTargetError("");
      try {
        let current = target;
        if (!current) {
          const created = await apiPost("/api/v1/architecture/targets", {
            current_fingerprint: fingerprint,
            idempotency_key:
              globalThis.crypto?.randomUUID?.() || `v2-drawer-${Date.now()}`,
            changes: [targetDraft],
          });
          current = architectureTargetResponseSchema.parse(created);
          setTarget(current);
        } else {
          const saved = await apiPost(
            `/api/v1/architecture/targets/${current.id}/changes`,
            {
              expected_revision: current.revision,
              current_fingerprint: fingerprint,
              changes: [targetDraft],
            },
          );
          current = architectureTargetResponseSchema.parse(saved);
          setTarget(current);
        }
        const submitted = await apiPost(
          `/api/v1/architecture/targets/${current.id}/submit`,
          {
            expected_revision: current.revision,
            actor: "owner",
            comment: "Architecture V2 offcanvas proposal",
          },
        );
        setTarget(architectureTargetResponseSchema.parse(submitted));
        setTargetResult(submitted);
      } catch (value) {
        setTargetError(
          value instanceof Error
            ? value.message
            : "TARGET submit request failed",
        );
      } finally {
        setTargetBusy(undefined);
      }
    };
  return (
    <main className="architecture-v2-preview">
      <Toolbar
        mode={mode}
        onMode={(next) => {
          setMode(next);
          replaceState();
        }}
        search={search}
        setSearch={setSearch}
        matches={matches}
        onSelectService={(service) => {
          setSearch("");
          if (level === "Platform") {
            setPlatformSelection(service);
            setInspector({ kind: "service", value: service });
            requestFocus(serviceID(service));
          } else navigateService(service);
        }}
        onReference={openReference}
        onPropose={openTargetProposal}
        onBack={() =>
          selectedOperation && selectedService
            ? navigateService(selectedService)
            : goPlatform()
        }
        level={level}
        service={selectedService}
        operation={selectedOperation}
        focus={focus}
        clearFocus={() => {
          setFocus("");
          replaceState();
        }}
        visibleCount={visibleCount}
        hiddenCount={services.length - visibleCount}
      />
      <section className="architecture-v2-workspace">
        <ArchitectureCanvas
          fingerprint={fingerprint}
          graph={graph}
          focusRequest={focusRequest}
          onInspector={onInspector}
          onDrillDown={drillDown}
          onClearInspector={() => {
            setPlatformSelection(undefined);
            setInspector(
              selectedOperation
                ? { kind: "operation", value: selectedOperation }
                : selectedService
                  ? { kind: "service", value: selectedService }
                  : { kind: "platform" },
            );
          }}
          onToggleGroup={toggleGroup}
        />
        <InspectorPanel
          inspector={inspector}
          catalog={catalog}
          onOpenService={navigateService}
          onOpenOperation={navigateOperation}
          onToggleGroup={(name) =>
            toggleGroup(
              name === "__unlinked__"
                ? { kind: "unlinked", value: inspector.value }
                : { kind: "group", value: { name } },
            )
          }
          onSelectRelation={(relation) =>
            setInspector({ kind: "relation", value: relation })
          }
          onFocus={(id) => {
            setFocus(id);
            requestFocus(id);
            replaceState();
          }}
          onPropose={openTargetProposal}
          level={level}
          selectedHidden={selectedHidden}
        />
        <ArchitectureTargetDrawer
          open={targetDrawerOpen}
          catalog={catalog as ArchitectureCatalog}
          draft={targetDraft}
          onDraftChange={setTargetDraft}
          onClose={() => setTargetDrawerOpen(false)}
          onSaveDraft={saveTargetDraft}
          onSubmit={submitTarget}
          busyAction={targetBusy}
          target={target}
          lifecycleError={targetError}
          lifecycleResult={targetResult}
          context={{
            serviceName:
              selectedService || platformSelection
                ? serviceName((selectedService || platformSelection)!)
                : undefined,
            operationName: selectedOperation
              ? operationIdentity(selectedOperation)
              : undefined,
            operationTransport: selectedOperation
              ? transport(selectedOperation)
              : undefined,
          }}
        />
      </section>
      {selectedService && !selectedOperation && (
        <div className="architecture-v2-group-toggle">
          <span>Operation grouping</span>
          <button
            className={groupBy === "capability" ? "primary" : "button"}
            onClick={() => {
              setGroupBy("capability");
              replaceState();
            }}
          >
            Capability
          </button>
          <button
            className={groupBy === "transport" ? "primary" : "button"}
            onClick={() => {
              setGroupBy("transport");
              replaceState();
            }}
          >
            Transport
          </button>
          <button className="button" onClick={openTargetProposal}>
            Propose a change in TARGET
          </button>
        </div>
      )}
    </main>
  );
}
