"use client";

import Link from "next/link";
import { formatDate } from "@/lib/api";
import type { Conversation } from "@/lib/schemas";

const scopeLabels: Record<string, string> = { workspace: "Workspace", project: "Проект", plan: "План", run: "Run", task: "Задача" };

export function ConversationList({ items, activeID }: { items: Conversation[]; activeID?: string }) {
  return <div className="conversation-list">{items.map(item => <Link className={item.id === activeID ? "conversation-link active" : "conversation-link"} href={`/control/${item.id}`} key={item.id}><strong>{item.title}</strong><span>{scopeLabels[item.scope_type] ?? item.scope_type} · {item.message_count} сообщений</span><small>{formatDate(item.updated_at)}</small></Link>)}{!items.length && <p className="muted">Диалогов пока нет.</p>}</div>;
}
