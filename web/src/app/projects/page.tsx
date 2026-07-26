"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { z } from "zod";
import { apiGet, formatDate, shortID } from "@/lib/api";
import { projectSchema, projectsSchema } from "@/lib/schemas";
import { DataTable } from "@/components/data-table";
import { Failure, Loading } from "@/components/page-state";
import { StatusBadge } from "@/components/status-badge";

type Project = z.infer<typeof projectSchema>;

export default function ProjectsPage() {
  const [search, setSearch] = useState("");
  const query = useQuery({ queryKey: ["projects"], queryFn: () => apiGet("/api/v1/projects", projectsSchema) });
  const columns = useMemo<ColumnDef<Project>[]>(() => [
    { header: "Проект", accessorKey: "name", cell: ({ row }) => <Link className="row-link" href={`/projects/${row.original.id}`}>{row.original.name}<small>{shortID(row.original.id)}</small></Link> },
    { header: "Роль", accessorKey: "repository_role" },
    { header: "Статус", accessorKey: "status", cell: ({ getValue }) => <StatusBadge status={String(getValue())} /> },
    { header: "Ветка", cell: ({ row }) => <span>{row.original.current_branch}<small>{row.original.is_dirty ? "Есть изменения" : "Чисто"}</small></span> },
    { header: "Commit", cell: ({ row }) => <code>{shortID(row.original.head_commit)}</code> },
    { header: "Обновлён", cell: ({ row }) => formatDate(row.original.updated_at) },
  ], []);
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const projects = query.data!.projects.filter(item => item.name.toLowerCase().includes(search.toLowerCase()));
  return <div className="page"><header><div><p className="eyebrow">Каталог</p><h1>Проекты</h1><p>{query.data!.projects.length} подключённых репозиториев.</p></div></header><div className="toolbar"><label>Поиск проекта<input value={search} onChange={event => setSearch(event.target.value)} placeholder="Название" /></label></div><section className="panel"><DataTable data={projects} columns={columns} /></section></div>;
}
