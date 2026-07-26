"use client";

import Link from "next/link";
import { FormEvent, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { apiGet, apiPost } from "@/lib/api";
import { planBundleSchema } from "@/lib/schemas";
import { Failure, Loading } from "@/components/page-state";

export default function RevisePlanPage() {
  const id = String(useParams().id);
  const router = useRouter();
  const plan = useQuery({ queryKey: ["plan", id], queryFn: () => apiGet(`/api/v1/plans/${id}`, planBundleSchema) });
  const [instruction, setInstruction] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const revised = planBundleSchema.parse(await apiPost(`/api/v1/plans/${id}/revisions`, { revision_instruction: instruction.trim() }));
      router.push(`/plans/${revised.plan.id}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Не удалось создать новую версию");
      setBusy(false);
    }
  }

  if (plan.isLoading) return <Loading />;
  if (plan.error) return <Failure error={plan.error} />;
  const bundle = plan.data!;
  if (bundle.plan.status !== "discussion") return <div className="page"><div className="alert"><strong>План нельзя изменить</strong><p>Новые версии разрешены только на этапе обсуждения. Согласованные и запущенные fingerprints неизменяемы.</p></div><Link href={`/plans/${id}`}>Вернуться к плану</Link></div>;
  return <div className="page wizard-page">
    <header><div><p className="eyebrow">План · версия {bundle.plan.version + 1}</p><h1>Уточнить план</h1><p>Текущая версия останется в истории и будет заменена новым проверенным draft.</p></div><Link className="button action-link" href={`/plans/${id}`}>Отмена</Link></header>
    <form className="wizard-layout" onSubmit={submit}>
      <div className="panel form-card"><fieldset disabled={busy}><legend>Корректировка владельца</legend><label>Что изменить<textarea required minLength={5} maxLength={10000} value={instruction} onChange={event => setInstruction(event.target.value)} placeholder="Например: разделить задачу frontend на две независимые задачи и добавить отдельный критерий проверки мобильного viewport." /></label><p className="field-help">Проекты сохранятся из текущей версии. Планировщик заново построит задачи, зависимости и fingerprint.</p></fieldset>{error && <div className="inline-error" role="alert">{error}</div>}<button className="primary form-submit" disabled={busy || instruction.trim().length < 5} type="submit">{busy ? "Создаём и проверяем версию…" : `Создать версию ${bundle.plan.version + 1}`}</button></div>
      <aside className="panel wizard-aside"><h2>Текущая версия {bundle.plan.version}</h2><p>{bundle.plan.summary}</p><dl><dt>Задач</dt><dd>{bundle.tasks.length}</dd><dt>Проектов</dt><dd>{new Set(bundle.tasks.map(task => task.project_id)).size}</dd><dt>Риск</dt><dd>{bundle.plan.risk_level}</dd></dl><p className="muted">Pending approval и предложенные work items старой версии будут аннулированы backend-транзакцией.</p></aside>
    </form>
  </div>;
}
