"use client";

import Link from "next/link";
import { FormEvent, useState } from "react";
import { useParams } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiGet, apiPost, formatDate } from "@/lib/api";
import { conversationDetailSchema, conversationsSchema, type ActionProposal } from "@/lib/schemas";
import { ConversationList } from "@/components/conversation-list";
import { Failure, Loading } from "@/components/page-state";
import { ResourceActions } from "@/components/resource-actions";
import { StatusBadge } from "@/components/status-badge";

function resourceHref(type: string, id: string) {
  return `/${type === "run" ? "runs" : `${type}s`}/${id}`;
}

export default function ConversationPage() {
  const id = String(useParams().id);
  const client = useQueryClient();
  const detail = useQuery({ queryKey: ["conversation", id], queryFn: () => apiGet(`/api/v1/conversations/${id}`, conversationDetailSchema) });
  const list = useQuery({ queryKey: ["conversations"], queryFn: () => apiGet("/api/v1/conversations", conversationsSchema) });
  const [content, setContent] = useState("");
  const send = useMutation({
    mutationFn: async () => conversationDetailSchema.parse(await apiPost(`/api/v1/conversations/${id}/messages`, { content: content.trim() })),
    onSuccess: value => {
      client.setQueryData(["conversation", id], value);
      setContent("");
      client.invalidateQueries({ queryKey: ["conversations"] });
    },
  });
  const decide = useMutation({
    mutationFn: ({ proposalID, status }: { proposalID: string; status: "confirmed" | "rejected" }) => apiPost(`/api/v1/action-proposals/${proposalID}/decision`, { status }),
    onSuccess: () => client.invalidateQueries({ queryKey: ["conversation", id] }),
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    if (content.trim()) send.mutate();
  }
  if (detail.isLoading || list.isLoading) return <Loading />;
  if (detail.error || list.error) return <Failure error={(detail.error || list.error)!} />;
  const value = detail.data!;
  const pending = value.proposals.filter(item => item.status === "pending");
  return (
    <div className="page control-page">
      <header>
        <div><p className="eyebrow">Центр управления · {value.conversation.scope_type}</p><h1>{value.conversation.title}</h1><p>{value.conversation.agent_thread_id ? "Контекст агента сохранён" : "Новый read-only контекст"} · обновлён {formatDate(value.conversation.updated_at)}</p></div>
        <Link className="button action-link" href="/control">Новый диалог</Link>
      </header>
      <div className="control-grid">
        <aside className="panel conversation-sidebar"><h2>Диалоги</h2><ConversationList items={list.data!.items} activeID={id} /></aside>
        <section className="panel chat-panel">
          <div className="message-timeline">
            {!value.messages.length && <div className="empty-chat"><strong>Задайте первый вопрос</strong><p>Можно спросить о проектах, topology, планах, run, задачах и согласованиях.</p></div>}
            {value.messages.map(message => (
              <article className={`chat-message message-${message.role}`} key={message.id}>
                <div className="message-meta"><strong>{message.role === "owner" ? "Вы" : "Оркестратор"}</strong><span>{formatDate(message.created_at)}</span></div>
                {message.status === "pending" ? <p className="thinking">Анализирую сохранённое состояние…</p> : message.status === "failed" ? <div className="inline-error">{message.error ?? "Ответ не сформирован"}</div> : <p className="message-content">{message.content}</p>}
                {message.references.length > 0 && <div className="message-references">{message.references.map(reference => <Link href={resourceHref(reference.resource_type, reference.resource_id)} key={`${reference.resource_type}:${reference.resource_id}`}>{reference.label}</Link>)}</div>}
                {value.proposals.filter(proposal => proposal.message_id === message.id).map(proposal => <ProposalCard proposal={proposal} key={proposal.id} onConfirmed={async () => { await decide.mutateAsync({ proposalID: proposal.id, status: "confirmed" }); }} onRejected={() => decide.mutate({ proposalID: proposal.id, status: "rejected" })} />)}
              </article>
            ))}
          </div>
          <form className="chat-composer" onSubmit={submit}>
            <label htmlFor="control-message">Сообщение владельца</label>
            <textarea id="control-message" required maxLength={12000} value={content} onChange={event => setContent(event.target.value)} placeholder="Спросите о состоянии или опишите, чем нужно управлять…" />
            <div><span>{content.length} / 12000</span><button className="primary" disabled={send.isPending || !content.trim()}>{send.isPending ? "Оркестратор анализирует…" : "Отправить"}</button></div>
            {send.error && <span className="inline-error">{send.error.message}</span>}
          </form>
        </section>
        <aside className="panel proposal-sidebar">
          <div className="panel-title"><div><h2>Предложения</h2><p>Требуют отдельного решения</p></div><strong>{pending.length}</strong></div>
          {pending.length ? pending.map(proposal => <div className="proposal-summary" key={proposal.id}><StatusBadge status={proposal.risk_level} /><strong>{proposal.title}</strong><Link href={resourceHref(proposal.resource_type, proposal.resource_id)}>Открыть ресурс</Link></div>) : <p className="muted">Нет ожидающих действий.</p>}
        </aside>
      </div>
    </div>
  );
}

function ProposalCard({ proposal, onConfirmed, onRejected }: { proposal: ActionProposal; onConfirmed: () => Promise<void>; onRejected: () => void }) {
  const active = proposal.status === "pending";
  return (
    <div className={`action-card action-${proposal.status}`}>
      <div><p className="eyebrow">Предлагаемое действие · {proposal.risk_level}</p><strong>{proposal.title}</strong><p>{proposal.description}</p><Link href={resourceHref(proposal.resource_type, proposal.resource_id)}>Открыть {proposal.resource_type}</Link></div>
      {active ? <div className="actions"><ResourceActions resourceType={proposal.resource_type as "plan" | "run" | "task"} resourceID={proposal.resource_id} fingerprint={proposal.fingerprint ?? undefined} actions={[{ action: proposal.action, requires_confirmation: true, requires_fingerprint: proposal.action === "approve" }]} onCompleted={onConfirmed} /><button className="button" onClick={onRejected}>Отклонить предложение</button></div> : <StatusBadge status={proposal.status} />}
    </div>
  );
}
