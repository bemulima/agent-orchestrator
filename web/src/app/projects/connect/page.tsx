"use client";

import Link from "next/link";
import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { apiPost } from "@/lib/api";
import { connectProjectResultSchema } from "@/lib/schemas";

const roles = [
  ["service", "Backend / сервис"],
  ["frontend", "Frontend"],
  ["infrastructure", "Инфраструктура"],
  ["content", "Контент"],
  ["policy", "Правила и промпты"],
  ["documentation", "Документация"],
  ["archive", "Архив"],
] as const;

export default function ConnectProjectPage() {
  const router = useRouter();
  const [source, setSource] = useState<"path" | "git_url">("path");
  const [value, setValue] = useState("");
  const [role, setRole] = useState("service");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<ReturnType<typeof connectProjectResultSchema.parse>>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const payload = { [source]: value.trim(), repository_role: role };
      setResult(connectProjectResultSchema.parse(await apiPost("/api/v1/projects/connect", payload)));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Не удалось подключить проект");
    } finally {
      setBusy(false);
    }
  }

  return <div className="page wizard-page">
    <header><div><p className="eyebrow">Каталог · новый источник</p><h1>Подключить проект</h1><p>Оркестратор проверит источник и выполнит безопасное read-only сканирование.</p></div><Link className="button action-link" href="/projects">Отмена</Link></header>
    <div className="wizard-layout">
      <form className="panel form-card" onSubmit={submit}>
        <fieldset disabled={busy}>
          <legend>1. Источник репозитория</legend>
          <div className="segmented" role="group" aria-label="Тип источника">
            <button className={source === "path" ? "selected" : ""} type="button" onClick={() => { setSource("path"); setValue(""); }}>Локальный путь</button>
            <button className={source === "git_url" ? "selected" : ""} type="button" onClick={() => { setSource("git_url"); setValue(""); }}>Git URL</button>
          </div>
          <label>{source === "path" ? "Путь внутри контейнера" : "URL репозитория"}<input required value={value} onChange={event => setValue(event.target.value)} placeholder={source === "path" ? "/projects/microservices/my-service" : "https://github.com/org/repository.git"} /></label>
          {source === "path" && <p className="field-help">Хост-каталог <code>PROJECTS_HOST_ROOT</code> доступен контейнеру как <code>/projects</code>.</p>}
        </fieldset>
        <fieldset disabled={busy}>
          <legend>2. Роль проекта</legend>
          <label>Назначение репозитория<select value={role} onChange={event => setRole(event.target.value)}>{roles.map(([key, label]) => <option value={key} key={key}>{label}</option>)}</select></label>
        </fieldset>
        {error && <div className="inline-error" role="alert">{error}</div>}
        <button className="primary form-submit" disabled={busy || !value.trim()} type="submit">{busy ? "Проверяем и сканируем…" : "Подключить и сканировать"}</button>
      </form>
      <aside className="panel wizard-aside">
        <h2>{result ? "Проект подключён" : "Что произойдёт"}</h2>
        {!result ? <ol><li>Источник будет проверен по allowlist.</li><li>Репозиторий добавится в каталог.</li><li>Discovery определит стек и зависимости.</li><li>Запись станет доступна при создании планов.</li></ol> : <div className="result-card"><strong>{result.project.name}</strong><span>{result.project.repository_role} · {result.project.status}</span><dl><dt>Тип</dt><dd>{String(result.snapshot.service_kind ?? "—")}</dd><dt>Язык</dt><dd>{String(result.snapshot.language ?? "—")}</dd><dt>Framework</dt><dd>{String(result.snapshot.framework ?? "—")}</dd></dl>{result.report.warnings.length > 0 && <div className="warning-list"><strong>Предупреждения</strong><ul>{result.report.warnings.map(item => <li key={item}>{item}</li>)}</ul></div>}<button className="primary" onClick={() => router.push(`/projects/${result.project.id}`)}>Открыть проект</button></div>}
      </aside>
    </div>
  </div>;
}
