"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiPost } from "@/lib/api";
import type { ResourceAction } from "@/lib/schemas";

const labels: Record<string, string> = {
  prepare_issues: "Подготовить issues", submit: "Отправить на согласование", approve: "Подтвердить",
  reject: "Отклонить", publish_issues: "Опубликовать issues", run: "Запустить", pause: "Приостановить",
  resume: "Продолжить", cancel: "Отменить", retry: "Повторить",
};

function request(resourceType: "plan" | "run" | "task", resourceID: string, action: string, fingerprint?: string) {
  if (resourceType === "plan") {
    const path: Record<string, string> = {
      prepare_issues: "issues/prepare", submit: "submit", approve: "approve", reject: "reject",
      publish_issues: "issues/publish", run: "run",
    };
    const body = ["submit", "approve", "reject"].includes(action)
      ? { actor: "owner", ...(action === "approve" ? { fingerprint } : {}) }
      : undefined;
    return apiPost(`/api/v1/plans/${resourceID}/${path[action]}`, body);
  }
  return apiPost(`/api/v1/${resourceType === "run" ? "runs" : "tasks"}/${resourceID}/${action}`);
}

export function ResourceActions({ resourceType, resourceID, actions, fingerprint, onCompleted }: {
  resourceType: "plan" | "run" | "task";
  resourceID: string;
  actions: ResourceAction[];
  fingerprint?: string;
  onCompleted?: (action: string) => void | Promise<void>;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (action: string) => request(resourceType, resourceID, action, fingerprint),
    onSuccess: async (_, action) => {
      await onCompleted?.(action);
      await client.invalidateQueries();
    },
  });
  if (!actions.length) return null;
  return (
    <div className="actions">
      {actions.map(item => <button key={item.action} className={item.action === "approve" ? "primary" : "button"} disabled={mutation.isPending} onClick={() => {
        const warning = item.requires_fingerprint ? `Будет подтверждён fingerprint ${fingerprint}.` : "Действие изменит состояние ресурса.";
        if (!item.requires_confirmation || window.confirm(`${labels[item.action] ?? item.action}?\n\n${warning}`)) mutation.mutate(item.action);
      }}>{labels[item.action] ?? item.action}</button>)}
      {mutation.error && <span className="inline-error">{mutation.error.message}</span>}
    </div>
  );
}
