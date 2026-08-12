"use client";

import { useParams } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiGet, apiPost, formatDate } from "@/lib/api";
import { projectSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";
import { StatusBadge } from "@/components/status-badge";

export default function ProjectDetailPage() {
  const id = String(useParams().id);
  const client = useQueryClient();
  const query = useQuery({ queryKey: ["project", id], queryFn: () => apiGet(`/api/v1/projects/${id}`, projectSchema) });
  const lifecycle = useMutation({
    mutationFn: (action: "archive" | "restore") => apiPost(`/api/v1/projects/${id}/${action}`),
    onSettled: async () => client.invalidateQueries(),
  });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  const project = query.data!;
  const archived = project.status === "archived";
  const changeLifecycle = () => {
    const action = archived ? "restore" : "archive";
    const message = archived
      ? "Восстановить проект? Он снова появится в планировании и topology."
      : "Архивировать проект? История и discovery-снимки сохранятся, но новые операции будут недоступны до восстановления.";
    if (window.confirm(message)) lifecycle.mutate(action);
  };
  return <div className="page"><header><div><p className="eyebrow">Проект</p><h1>{project.name}</h1><p>{project.repository_role} · {project.current_branch}</p></div><div className="header-actions"><StatusBadge status={project.status} /><button className={archived ? "primary" : "button"} disabled={lifecycle.isPending || project.status === "scanning"} onClick={changeLifecycle}>{archived ? "Восстановить" : "Архивировать"}</button></div></header>{lifecycle.error && <p className="inline-error" role="alert">{lifecycle.error.message}</p>}{archived && <p className="notice">Проект исключён из новых планов, discovery, onboarding и topology. Сохранённое состояние: {project.archived_from_status ?? "—"}; архивирован {formatDate(project.archived_at)}.</p>}<section className="detail-grid"><article className="panel"><h2>Repository</h2><dl><dt>Default branch</dt><dd>{project.default_branch}</dd><dt>Current branch</dt><dd>{project.current_branch}</dd><dt>Commit</dt><dd><code>{project.head_commit}</code></dd><dt>Рабочее дерево</dt><dd>{project.is_dirty ? "Есть изменения" : "Чистое"}</dd><dt>Обновлено</dt><dd>{formatDate(project.updated_at)}</dd></dl></article><article className="panel"><h2>Источник</h2><p className="breakable">{project.local_path ?? project.git_url ?? "—"}</p><div className="link-stack"><a href={`/api/v1/projects/${id}/reports/latest`}>Discovery report</a><a href={`/api/v1/projects/${id}/dependencies`}>Dependencies JSON</a><a href={`/api/v1/projects/${id}/contracts`}>Contracts JSON</a><a href={`/api/v1/projects/${id}/consumers`}>Consumers JSON</a></div></article></section></div>;
}
