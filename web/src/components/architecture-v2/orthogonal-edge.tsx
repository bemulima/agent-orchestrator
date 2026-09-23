"use client";

import {
  BaseEdge,
  EdgeLabelRenderer,
  Position,
  type EdgeProps,
  useInternalNode,
} from "@xyflow/react";

/**
 * Data understood by `OrthogonalEdge`.  The graph builder can set a stable
 * `parallelIndex`/`parallelCount` when it collapses several documented
 * relations into a visual bundle.  The offset deliberately belongs to the
 * rendered path (rather than just its label), so parallel transports stay
 * independently selectable and readable.
 */
export type OrthogonalEdgeData = {
  kind?: string;
  parallelIndex?: number;
  parallelCount?: number;
  routeOffset?: number;
};

type NodeBox = { x: number; y: number; width: number; height: number };
type Point = { x: number; y: number };

const DEFAULT_WIDTH = 286;
const DEFAULT_HEIGHT = 94;
const EXIT_GAP = 28;
const PARALLEL_GAP = 16;

function nodeBox(
  node: ReturnType<typeof useInternalNode>,
): NodeBox | undefined {
  if (!node) return undefined;
  const width = node.measured.width || node.width || DEFAULT_WIDTH;
  const height = node.measured.height || node.height || DEFAULT_HEIGHT;
  return {
    x: node.internals.positionAbsolute.x + width / 2,
    y: node.internals.positionAbsolute.y + height / 2,
    width,
    height,
  };
}

function side(from: NodeBox, to: NodeBox): Position {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  // Horizontal first makes left-to-right component layouts read as a flow.
  // The vertical branch prevents a long U-turn when two nodes are stacked.
  if (Math.abs(dx) >= Math.abs(dy))
    return dx >= 0 ? Position.Right : Position.Left;
  return dy >= 0 ? Position.Bottom : Position.Top;
}

function anchor(box: NodeBox, position: Position): Point {
  switch (position) {
    case Position.Left:
      return { x: box.x - box.width / 2, y: box.y };
    case Position.Right:
      return { x: box.x + box.width / 2, y: box.y };
    case Position.Top:
      return { x: box.x, y: box.y - box.height / 2 };
    case Position.Bottom:
      return { x: box.x, y: box.y + box.height / 2 };
  }
}

function outside(point: Point, position: Position, gap: number): Point {
  switch (position) {
    case Position.Left:
      return { x: point.x - gap, y: point.y };
    case Position.Right:
      return { x: point.x + gap, y: point.y };
    case Position.Top:
      return { x: point.x, y: point.y - gap };
    case Position.Bottom:
      return { x: point.x, y: point.y + gap };
  }
}

function compact(points: Point[]) {
  return points.filter((point, index) => {
    const previous = points[index - 1];
    return !previous || previous.x !== point.x || previous.y !== point.y;
  });
}

function pathFrom(points: Point[]) {
  return compact(points)
    .map((point, index) => `${index ? "L" : "M"}${point.x},${point.y}`)
    .join(" ");
}

function parallelOffset(data: OrthogonalEdgeData | undefined) {
  if (typeof data?.routeOffset === "number") return data.routeOffset;
  const count = Math.max(1, Number(data?.parallelCount || 1));
  const index = Math.min(
    count - 1,
    Math.max(0, Number(data?.parallelIndex || 0)),
  );
  return (index - (count - 1) / 2) * PARALLEL_GAP;
}

function middleSegment(points: Point[]) {
  let best = { from: points[0], to: points[points.length - 1], length: -1 };
  for (let index = 1; index < points.length; index += 1) {
    const from = points[index - 1];
    const to = points[index];
    const length = Math.abs(to.x - from.x) + Math.abs(to.y - from.y);
    if (length > best.length) best = { from, to, length };
  }
  return { x: (best.from.x + best.to.x) / 2, y: (best.from.y + best.to.y) / 2 };
}

/**
 * Render a route made exclusively of horizontal and vertical segments.
 *
 * The first and last stubs always leave their node through the semantically
 * closest side.  The middle channel is offset for parallel edges, keeping a
 * bundle legible without changing the underlying source/target relation.
 */
export function OrthogonalEdge(props: EdgeProps) {
  const sourceNode = useInternalNode(props.source);
  const targetNode = useInternalNode(props.target);
  const source = nodeBox(sourceNode);
  const target = nodeBox(targetNode);
  if (!source || !target) return null;

  const sourcePosition = side(source, target);
  const targetPosition = side(target, source);
  const sourcePoint = anchor(source, sourcePosition);
  const targetPoint = anchor(target, targetPosition);
  const offset = parallelOffset(props.data as OrthogonalEdgeData | undefined);
  const sourceExit = outside(sourcePoint, sourcePosition, EXIT_GAP);
  const targetEntry = outside(targetPoint, targetPosition, EXIT_GAP);
  const horizontal =
    sourcePosition === Position.Left || sourcePosition === Position.Right;

  // A four-corner channel prevents accidental diagonal shortcuts.  For
  // horizontally adjacent nodes the channel is a vertical spine; for stacked
  // nodes it is a horizontal spine.  The exit gaps keep the path outside both
  // endpoint boxes, including when the boxes are close or overlap slightly.
  const channel = horizontal
    ? (sourceExit.x + targetEntry.x) / 2 + offset
    : (sourceExit.y + targetEntry.y) / 2 + offset;
  const points = horizontal
    ? [
        sourcePoint,
        sourceExit,
        { x: channel, y: sourceExit.y },
        { x: channel, y: targetEntry.y },
        targetEntry,
        targetPoint,
      ]
    : [
        sourcePoint,
        sourceExit,
        { x: sourceExit.x, y: channel },
        { x: targetEntry.x, y: channel },
        targetEntry,
        targetPoint,
      ];
  const path = pathFrom(points);
  const labelPoint = middleSegment(compact(points));
  const kind = String(
    (props.data as OrthogonalEdgeData | undefined)?.kind || "relation",
  );

  return (
    <>
      <BaseEdge
        data-testid={`architecture-edge-${props.id}`}
        data-route="orthogonal"
        data-transport={kind}
        data-segments={compact(points).length - 1}
        path={path}
        markerEnd={props.markerEnd}
        interactionWidth={props.interactionWidth || 26}
        className={`architecture-v2-edge architecture-v2-edge-${kind} architecture-v2-edge-orthogonal ${props.selected ? "is-selected" : ""}`}
        style={{ ...props.style, pointerEvents: "stroke" }}
      />
      {props.label ? (
        <EdgeLabelRenderer>
          <div
            className={`architecture-v2-edge-label architecture-v2-edge-label-${kind}`}
            data-testid={`architecture-edge-label-${props.id}`}
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${labelPoint.x}px, ${labelPoint.y}px)`,
              pointerEvents: "none",
            }}
          >
            {props.label}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  );
}

export const orthogonalEdgeTypes = { orthogonal: OrthogonalEdge };
