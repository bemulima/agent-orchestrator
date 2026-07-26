"use client";

import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { apiGet, apiPost } from "@/lib/api";
import { conversationSchema, conversationsSchema, plansPageSchema, projectsSchema } from "@/lib/schemas";
import { ConversationList } from "@/components/conversation-list";
import { Failure, Loading } from "@/components/page-state";

export default function ControlPage() {
  const router = useRouter();
  const client = useQueryClient();
  const conversations = useQuery({ queryKey: ["conversations"], queryFn: () => apiGet("/api/v1/conversations", conversationsSchema) });
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => apiGet("/api/v1/projects", projectsSchema) });
  const plans = useQuery({ queryKey: ["plans"], queryFn: () => apiGet("/api/v1/plans?limit=100", plansPageSchema) });
  const [title, setTitle] = useState("");
  const [scopeType, setScopeType] = useState("workspace");
  const [scopeID, setScopeID] = useState("");
  const resources = useMemo(() => scopeType === "project" ? projects.data?.projects.map(item => [item.id, item.name]) ?? [] : scopeType === "plan" ? plans.data?.items.map(item => [item.id, `v${item.version} · ${item.summary}`]) ?? [] : [], [scopeType, projects.data, plans.data]);
  const create = useMutation({
    mutationFn: async () => conversationSchema.parse(await apiPost("/api/v1/conversations", { title: title.trim(), scope_type: scopeType, ...(scopeType === "workspace" ? {} : { scope_id: scopeID }) })),
    onSuccess: async value => { await client.invalidateQueries({ queryKey: ["conversations"] }); router.push(`/control/${value.id}`); },
  });

  function submit(event: FormEvent) { event.preventDefault(); create.mutate(); }
  if (conversations.isLoading || projects.isLoading || plans.isLoading) return <Loading />;
  if (conversations.error || projects.error || plans.error) return <Failure error={(conversations.error || projects.error || plans.error)!} />;
  return <div className="page control-page"><header><div><p className="eyebrow">Диалоговый контур</p><h1>Центр управления</h1><p>Обсуждайте состояние платформы и получайте безопасные предложения действий.</p></div></header><div className="control-grid"><aside className="panel conversation-sidebar"><div className="panel-title"><div><h2>Диалоги</h2><p>{conversations.data!.items.length} сохранено</p></div></div><ConversationList items={conversations.data!.items} /></aside><section className="panel control-welcome"><div><p className="eyebrow">Новый диалог</p><h2>С чего начнём?</h2><p className="muted">Выберите контекст. Агент будет читать persisted state и не сможет выполнить изменение без отдельной кнопки.</p></div><form className="control-create" onSubmit={submit}><label>Название<input required maxLength={255} value={title} onChange={event => setTitle(event.target.value)} placeholder="Например: разобраться с остановленным планом" /></label><label>Контекст<select value={scopeType} onChange={event => { setScopeType(event.target.value); setScopeID(""); }}><option value="workspace">Весь workspace</option><option value="project">Конкретный проект</option><option value="plan">Конкретный план</option></select></label>{scopeType !== "workspace" && <label>Ресурс<select required value={scopeID} onChange={event => setScopeID(event.target.value)}><option value="">Выберите…</option>{resources.map(([id, label]) => <option value={id} key={id}>{label}</option>)}</select></label>}{create.error && <span className="inline-error">{create.error.message}</span>}<button className="primary form-submit" disabled={create.isPending || !title.trim() || (scopeType !== "workspace" && !scopeID)}>{create.isPending ? "Создаём…" : "Начать диалог"}</button></form><div className="prompt-examples"><span>Примеры</span><p>Почему остановился последний план?</p><p>Какие проекты затронет изменение авторизации?</p><p>Что сейчас требует моего внимания?</p></div></section><aside className="panel control-rules"><h2>Границы безопасности</h2><ul><li>Ответы основаны на сохранённом состоянии.</li><li>Агент работает только на чтение.</li><li>Действия проходят backend state machine.</li><li>Каждое изменение требует подтверждения.</li></ul></aside></div></div>;
}
