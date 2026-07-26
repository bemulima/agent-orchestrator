"use client";

import Link from "next/link";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { apiGet, formatDate, shortID } from "@/lib/api";
import { runsPageSchema, type RunSummary } from "@/lib/schemas";
import { DataTable } from "@/components/data-table";
import { Failure, Loading } from "@/components/page-state";
import { ResourceActions } from "@/components/resource-actions";
import { StatusBadge } from "@/components/status-badge";

export default function RunsPage() {
  const query = useQuery({ queryKey: ["runs"], queryFn: () => apiGet("/api/v1/runs?limit=100", runsPageSchema) });
  const columns = useMemo<ColumnDef<RunSummary>[]>(() => [
    { header: "Выполнение", cell: ({ row }) => <Link className="row-link" href={`/runs/${row.original.id}`}>{row.original.plan_summary}<small>{shortID(row.original.id)}</small></Link> },
    { header: "Статус", cell: ({ row }) => <StatusBadge status={row.original.status} /> },
    { header: "Прогресс", cell: ({ row }) => `${row.original.completed_tasks} / ${row.original.task_count}` },
    { header: "Активно", accessorKey: "active_tasks" },
    { header: "Начато", cell: ({ row }) => formatDate(row.original.started_at) },
    { header: "Действия", cell: ({ row }) => <ResourceActions resourceType="run" resourceID={row.original.id} actions={row.original.allowed_actions} /> },
  ], []);
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  return <div className="page"><header><div><p className="eyebrow">Temporal</p><h1>Выполнение</h1><p>Запуски планов и управление workflow.</p></div></header><section className="panel"><DataTable data={query.data!.items} columns={columns} /></section></div>;
}
