"use client";

import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate } from "@/lib/api";
import { projectSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";
import { StatusBadge } from "@/components/status-badge";

export default function ProjectDetailPage() {
  const id = String(useParams().id);
  const query = useQuery({ queryKey: ["project", id], queryFn: () => apiGet(`/api/v1/projects/${id}`, projectSchema) });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const project = query.data!;
  return <div className="page"><header><div><p className="eyebrow">Проект</p><h1>{project.name}</h1><p>{project.repository_role} · {project.current_branch}</p></div><StatusBadge status={project.status} /></header><section className="detail-grid"><article className="panel"><h2>Repository</h2><dl><dt>Default branch</dt><dd>{project.default_branch}</dd><dt>Current branch</dt><dd>{project.current_branch}</dd><dt>Commit</dt><dd><code>{project.head_commit}</code></dd><dt>Рабочее дерево</dt><dd>{project.is_dirty ? "Есть изменения" : "Чистое"}</dd><dt>Обновлено</dt><dd>{formatDate(project.updated_at)}</dd></dl></article><article className="panel"><h2>Источник</h2><p className="breakable">{project.local_path ?? project.git_url ?? "—"}</p><div className="link-stack"><a href={`/api/v1/projects/${id}/reports/latest`}>Discovery report</a><a href={`/api/v1/projects/${id}/dependencies`}>Dependencies JSON</a><a href={`/api/v1/projects/${id}/contracts`}>Contracts JSON</a><a href={`/api/v1/projects/${id}/consumers`}>Consumers JSON</a></div></article></section></div>;
}
