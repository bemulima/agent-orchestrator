"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { apiGet, formatDate } from "@/lib/api";
import { approvalsPageSchema } from "@/lib/schemas";
import { Empty, Failure, Loading } from "@/components/page-state";
import { StatusBadge } from "@/components/status-badge";

export default function ApprovalsPage() {
  const query = useQuery({ queryKey: ["approvals"], queryFn: () => apiGet("/api/v1/approvals?limit=100", approvalsPageSchema) });
  if (query.isLoading) return <Loading />;
  if (query.error) return <Failure error={query.error} />;
  return <div className="page"><header><div><p className="eyebrow">Owner gate</p><h1>Согласования</h1><p>Решения, которые требуют явного подтверждения владельца.</p></div></header><section className="panel">{!query.data!.items.length ? <Empty /> : <div className="list">{query.data!.items.map(item => <div className="approval-row" key={item.id}><div><strong>{item.resource_name}</strong><small>{item.resource_type} · {item.action} · {formatDate(item.requested_at)}</small></div><StatusBadge status={item.status} /><div><span className="risk">{item.risk_level || "—"}</span>{item.resource_type === "plan" && <Link href={`/plans/${item.resource_id}`}>Открыть план</Link>}</div>{item.fingerprint && <code className="fingerprint">{item.fingerprint}</code>}</div>)}</div>}</section></div>;
}
