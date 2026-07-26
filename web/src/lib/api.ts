import { z } from "zod";

export class APIError extends Error {
  constructor(public status: number, message: string, public code = "request_failed") { super(message); }
}

export async function apiGet<T>(path: string, schema: z.ZodType<T>): Promise<T> {
  const response = await fetch(path, { headers: { Accept: "application/json" }, cache: "no-store" });
  if (!response.ok) throw await apiError(response);
  return schema.parse(await response.json());
}

export async function apiPost(path: string, body?: unknown): Promise<unknown> {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) throw await apiError(response);
  return response.json();
}

async function apiError(response: Response) {
  try {
    const data = await response.json() as { error?: { code?: string; message?: string } };
    return new APIError(response.status, data.error?.message ?? `HTTP ${response.status}`, data.error?.code);
  } catch {
    return new APIError(response.status, `HTTP ${response.status}`);
  }
}

export function formatDate(value?: string | null) {
  return value ? new Intl.DateTimeFormat("ru-RU", { dateStyle: "short", timeStyle: "short" }).format(new Date(value)) : "—";
}

export function shortID(value: string) { return value.slice(0, 8); }
