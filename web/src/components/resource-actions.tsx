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

export function ResourceActions({ resourceType, resourceID, actions, fingerprint, onCompleted, confirmations, actionLabels }: {
  resourceType: "plan" | "run" | "task";
  resourceID: string;
  actions: ResourceAction[];
  fingerprint?: string;
  onCompleted?: (action: string) => void | Promise<void>;
  confirmations?: Partial<Record<string, string>>;
  actionLabels?: Partial<Record<string, string>>;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (action: string) => request(resourceType, resourceID, action, fingerprint),
    onSuccess: async (_, action) => {
      await onCompleted?.(action);
    },
    onSettled: async () => client.invalidateQueries(),
  });
  if (!actions.length) return null;
  return (
    <div className="actions">
      {actions.map(item => <button key={item.action} className={item.action === "approve" ? "primary" : "button"} disabled={mutation.isPending} onClick={() => {
        const warning = confirmations?.[item.action] ?? (item.requires_fingerprint ? `Будет подтверждён fingerprint ${fingerprint}.` : "Действие изменит состояние ресурса.");
        const label = actionLabels?.[item.action] ?? labels[item.action] ?? item.action;
        if (!item.requires_confirmation || window.confirm(`${label}?\n\n${warning}`)) mutation.mutate(item.action);
      }}>{actionLabels?.[item.action] ?? labels[item.action] ?? item.action}</button>)}
      {mutation.error && <span className="inline-error" role="alert">{friendlyActionError(mutation.error.message)}</span>}
    </div>
  );
}

function friendlyActionError(message: string) {
  if (message.includes("planning resource already exists") || message.includes("conflict")) {
    return "Часть результата уже сохранена или запись изменилась. Список обновлён; повторите действие только для оставшихся issues.";
  }
  if (message.includes("approved issue proposal")) return "Для каждой задачи нужно сначала подготовить и одобрить issue-предложение.";
  return message;
}
