"use client";

import Link from "next/link";
import { FormEvent, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { apiGet, apiPost } from "@/lib/api";
import { commandSchema, planBundleSchema, projectsSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";

export default function NewPlanPage() {
  const router = useRouter();
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => apiGet("/api/v1/projects", projectsSchema) });
  const [goal, setGoal] = useState("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const visible = useMemo(() => projects.data?.projects.filter(project => project.name.toLowerCase().includes(search.toLowerCase())) ?? [], [projects.data, search]);

  function toggle(id: string) {
    setSelected(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id]);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const command = commandSchema.parse(await apiPost("/api/v1/commands", { text: goal.trim() }));
      const plan = planBundleSchema.parse(await apiPost(`/api/v1/commands/${command.id}/plan`, { project_ids: selected }));
      router.push(`/plans/${plan.plan.id}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Не удалось создать план");
      setBusy(false);
    }
  }

  if (projects.isLoading) return <Loading />;
  if (projects.error) return <Failure error={projects.error} />;
  return <div className="page wizard-page">
    <header><div><p className="eyebrow">Планирование · новый план</p><h1>Создать план</h1><p>Опишите результат и ограничьте область конкретными проектами.</p></div><Link className="button action-link" href="/plans">Отмена</Link></header>
    <form className="wizard-layout" onSubmit={submit}>
      <div className="panel form-card">
        <fieldset disabled={busy}>
          <legend>1. Цель</legend>
          <label>Что нужно изменить<textarea required minLength={10} value={goal} onChange={event => setGoal(event.target.value)} placeholder="Например: добавить безопасное версионное редактирование планов с сохранением истории и fingerprint…" /></label>
          <p className="field-help">Укажите ожидаемый результат, ограничения и что точно не должно изменяться.</p>
        </fieldset>
        <fieldset disabled={busy}>
          <legend>2. Проекты</legend>
          <label>Поиск<input value={search} onChange={event => setSearch(event.target.value)} placeholder="Название проекта" /></label>
          <div className="project-picker">{visible.map(project => <label className={selected.includes(project.id) ? "project-option selected" : "project-option"} key={project.id}><input type="checkbox" checked={selected.includes(project.id)} onChange={() => toggle(project.id)} /><span><strong>{project.name}</strong><small>{project.repository_role} · {project.current_branch}</small></span></label>)}</div>
          {!projects.data!.projects.length && <p className="muted">Сначала <Link href="/projects/connect">подключите проект</Link>.</p>}
        </fieldset>
        {error && <div className="inline-error" role="alert">{error}</div>}
        <button className="primary form-submit" disabled={busy || goal.trim().length < 10 || selected.length === 0} type="submit">{busy ? "Агент строит и проверяет план…" : "Сгенерировать черновик"}</button>
      </div>
      <aside className="panel wizard-aside"><h2>Область плана</h2><div className="selection-count"><strong>{selected.length}</strong><span>проектов выбрано</span></div><p>Планировщик прочитает topology, построит задачи и зависимости, проверит write scope и рассчитает fingerprint.</p><p className="muted">Генерация может занять несколько минут. Запуск работ начнётся только после согласования.</p></aside>
    </form>
  </div>;
}
