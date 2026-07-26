"use client";

import Link from "next/link";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { apiGet, formatDate, shortID } from "@/lib/api";
import { plansPageSchema, type PlanSummary } from "@/lib/schemas";
import { DataTable } from "@/components/data-table";
import { Failure, Loading } from "@/components/page-state";
import { ResourceActions } from "@/components/resource-actions";
import { StatusBadge } from "@/components/status-badge";

export default function PlansPage() {
  const query = useQuery({ queryKey: ["plans"], queryFn: () => apiGet("/api/v1/plans?limit=100", plansPageSchema) });
  const columns = useMemo<ColumnDef<PlanSummary>[]>(() => [
    { header: "План", cell: ({ row }) => <Link className="row-link" href={`/plans/${row.original.id}`}>{row.original.summary}<small>{shortID(row.original.id)} · v{row.original.version}</small></Link> },
    { header: "Статус", cell: ({ row }) => <StatusBadge status={row.original.status} /> },
    { header: "Риск", accessorKey: "risk_level" },
    { header: "Прогресс", cell: ({ row }) => <span>{row.original.completed_tasks} / {row.original.task_count}<small>{row.original.attention_tasks ? `${row.original.attention_tasks} требуют внимания` : ""}</small></span> },
    { header: "Обновлён", cell: ({ row }) => formatDate(row.original.updated_at) },
    { header: "Действия", cell: ({ row }) => <ResourceActions resourceType="plan" resourceID={row.original.id} fingerprint={row.original.fingerprint} actions={row.original.allowed_actions} /> },
  ], []);
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  return <div className="page"><header><div><p className="eyebrow">Планирование</p><h1>Планы</h1><p>Версии, согласования и прогресс задач.</p></div><Link className="primary action-link" href="/plans/new">Создать план</Link></header><section className="panel"><DataTable data={query.data!.items} columns={columns} /></section></div>;
}
