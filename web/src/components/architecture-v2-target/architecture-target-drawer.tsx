"use client";

import { useMemo, useState } from "react";
import type { ArchitectureCatalog, ArchitectureTarget } from "@/lib/schemas";
import {
  targetHumanSummary,
  targetOperationOptions,
  targetServiceOptions,
} from "./target-adapter";
import { TargetAdvancedJSON } from "./target-advanced-json";
import { TargetVisualDiff } from "./target-visual-diff";
import type { TargetChangeDraft, TargetLifecycleAction } from "./types";

/**
 * A deliberately small, controlled TARGET entry point for the CURRENT canvas.
 *
 * It owns no persistence and makes no architectural mutation by itself: the
 * explorer supplies an already-contextualized draft and wires save/submit into
 * the existing TARGET lifecycle API. This keeps CURRENT exploration read-only.
 */
export type ArchitectureTargetDrawerProps = {
  open: boolean;
  catalog: ArchitectureCatalog;
  draft: TargetChangeDraft;
  onDraftChange: (draft: TargetChangeDraft) => void;
  onClose: () => void;
  onSaveDraft?: () => void;
  onSubmit?: () => void;
  busyAction?: TargetLifecycleAction;
  disabled?: boolean;
  lifecycleError?: string;
  lifecycleResult?: unknown;
  target?: ArchitectureTarget;
  /** Human-readable source context; IDs remain in Advanced only. */
  context?: {
    serviceName?: string;
    operationName?: string;
    operationTransport?: string;
  };
};

function updateDraft(
  draft: TargetChangeDraft,
  patch: Partial<TargetChangeDraft>,
): TargetChangeDraft {
  return { ...draft, ...patch };
}

/**
 * The explorer can use this to create a draft without exposing catalog IDs in
 * its normal UI. The returned shape is exactly the established TARGET payload.
 */
export function targetDrawerContextDraft(
  serviceID?: string,
  operationID?: string,
): TargetChangeDraft {
  return {
    scope: operationID ? "operation" : "service",
    action: "change",
    kind: operationID ? "operation" : "responsibility",
    affected_service_ids: serviceID ? [serviceID] : [],
    affected_operation_ids: operationID ? [operationID] : [],
    rationale: { summary: "", details: "" },
    desired_result: { summary: "", success_criteria: [] },
    evidence: [],
    confidence: 0.8,
    unresolved_areas: [],
  };
}

export function ArchitectureTargetDrawer({
  open,
  catalog,
  draft,
  onDraftChange,
  onClose,
  onSaveDraft,
  onSubmit,
  busyAction,
  disabled,
  lifecycleError,
  lifecycleResult,
  target,
  context,
}: ArchitectureTargetDrawerProps) {
  const [advanced, setAdvanced] = useState(false);
  const serviceID = draft.affected_service_ids[0] || "";
  const operationID = draft.affected_operation_ids[0] || "";
  const services = useMemo(() => targetServiceOptions(catalog), [catalog]);
  const operations = useMemo(
    () => targetOperationOptions(catalog, serviceID),
    [catalog, serviceID],
  );
  const set = (patch: Partial<TargetChangeDraft>) =>
    onDraftChange(updateDraft(draft, patch));
  const contextualName = [
    context?.serviceName,
    context?.operationTransport && context?.operationName
      ? context.operationTransport
      : "",
    context?.operationName,
  ]
    .filter(Boolean)
    .join(" · ");

  if (!open) return null;

  return (
    <aside
      aria-label="TARGET change drawer"
      data-testid="target-change-drawer"
      style={{
        position: "absolute",
        zIndex: 12,
        top: 12,
        left: 12,
        bottom: 12,
        width: "min(440px, calc(100% - 24px))",
        overflow: "auto",
        padding: 16,
        background: "var(--panel, #fff)",
        border: "1px solid var(--border, #d8e0da)",
        borderRadius: 14,
        boxShadow: "0 12px 40px rgba(15, 23, 42, .20)",
        display: "grid",
        alignContent: "start",
        gap: 14,
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "start",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <div>
          <p className="eyebrow">TARGET · PROPOSAL</p>
          <h2 style={{ margin: "2px 0 6px" }}>Предложить изменение</h2>
          <p className="muted" style={{ margin: 0 }}>
            CURRENT остаётся только для чтения. Эта форма создаёт безопасный
            черновик TARGET.
          </p>
        </div>
        <button
          className="button"
          aria-label="Close target proposal"
          onClick={onClose}
        >
          Закрыть
        </button>
      </header>

      {contextualName && (
        <section
          style={{
            padding: 10,
            borderRadius: 10,
            background: "var(--surface-muted, #f2f6f3)",
          }}
        >
          <strong>Контекст из canvas</strong>
          <br />
          <span>{contextualName}</span>
        </section>
      )}

      <label>
        Что меняется?
        <input
          aria-label="What changes"
          disabled={disabled}
          value={draft.rationale.summary}
          onChange={(event) =>
            set({
              rationale: { ...draft.rationale, summary: event.target.value },
            })
          }
          placeholder="Кратко опишите предложенное изменение"
        />
      </label>
      <label>
        Тип изменения
        <select
          aria-label="Change action"
          disabled={disabled}
          value={draft.action}
          onChange={(event) =>
            set({ action: event.target.value as TargetChangeDraft["action"] })
          }
        >
          <option value="add">Добавить</option>
          <option value="change">Изменить</option>
          <option value="remove">Убрать</option>
        </select>
      </label>
      <label>
        Почему это нужно?
        <textarea
          aria-label="Why change"
          disabled={disabled}
          value={draft.rationale.details}
          onChange={(event) =>
            set({
              rationale: { ...draft.rationale, details: event.target.value },
            })
          }
          placeholder="Бизнес- или техническая причина; укажите известные ограничения"
        />
      </label>
      <label>
        Ожидаемый результат
        <input
          aria-label="Expected result"
          disabled={disabled}
          value={draft.desired_result.summary}
          onChange={(event) =>
            set({
              desired_result: {
                ...draft.desired_result,
                summary: event.target.value,
              },
            })
          }
          placeholder="Какой наблюдаемый результат должен появиться"
        />
      </label>
      <label>
        Критерии успеха — по одному на строку
        <textarea
          aria-label="Success criteria"
          disabled={disabled}
          value={draft.desired_result.success_criteria.join("\n")}
          onChange={(event) =>
            set({
              desired_result: {
                ...draft.desired_result,
                success_criteria: event.target.value
                  .split("\n")
                  .map((value) => value.trim())
                  .filter(Boolean),
              },
            })
          }
          placeholder="Например: клиент получает документированный ответ"
        />
      </label>

      <details>
        <summary>Изменить охват</summary>
        <div style={{ display: "grid", gap: 10, marginTop: 10 }}>
          <label>
            Сервис
            <select
              aria-label="Target service"
              disabled={disabled}
              value={serviceID}
              onChange={(event) =>
                set({
                  affected_service_ids: event.target.value
                    ? [event.target.value]
                    : [],
                  affected_operation_ids: [],
                })
              }
            >
              <option value="">Выберите сервис…</option>
              {services.map((service) => (
                <option key={service.id} value={service.id}>
                  {service.label} — {service.purpose}
                </option>
              ))}
            </select>
          </label>
          <label>
            Операция
            <select
              aria-label="Target operation"
              disabled={disabled || !serviceID}
              value={operationID}
              onChange={(event) =>
                set({
                  scope: event.target.value ? "operation" : "service",
                  affected_operation_ids: event.target.value
                    ? [event.target.value]
                    : [],
                })
              }
            >
              <option value="">Весь сервис</option>
              {operations.map((operation) => (
                <option key={operation.id} value={operation.id}>
                  {operation.transport} · {operation.label} —{" "}
                  {operation.businessTask}
                </option>
              ))}
            </select>
          </label>
        </div>
      </details>

      <section
        aria-label="Target change summary"
        style={{
          padding: 10,
          border: "1px solid var(--border, #d8e0da)",
          borderRadius: 10,
        }}
      >
        <strong>Предпросмотр</strong>
        <p style={{ margin: "6px 0 0" }}>
          {targetHumanSummary(draft, catalog) || "Укажите охват изменения"}
        </p>
      </section>
      <section data-testid="target-impact" aria-label="Target impact preview">
        <h3>Impact</h3>
        <p className="muted">
          The preview contains only the changed context and confirmed one-hop
          CURRENT neighbours. Unknown relations remain unknown.
        </p>
        <TargetVisualDiff catalog={catalog} changes={[draft]} />
      </section>

      <div style={{ display: "flex", flexWrap: "wrap", gap: 8 }}>
        <button
          className="button"
          disabled={disabled || busyAction === "save" || !onSaveDraft}
          onClick={onSaveDraft}
        >
          Сохранить черновик
        </button>
        <button
          className="primary"
          disabled={disabled || busyAction === "submit" || !onSubmit}
          onClick={onSubmit}
        >
          Отправить proposal
        </button>
      </div>
      {lifecycleError && (
        <p className="warning" role="alert">
          {lifecycleError}
        </p>
      )}

      <details
        open={advanced}
        onToggle={(event) =>
          setAdvanced((event.target as HTMLDetailsElement).open)
        }
      >
        <summary>Advanced / debug</summary>
        <p className="muted">
          Идентификаторы, исходный payload и результат API предназначены только
          для диагностики.
        </p>
        <TargetAdvancedJSON
          target={target}
          changes={[draft]}
          result={lifecycleResult}
        />
      </details>
    </aside>
  );
}
