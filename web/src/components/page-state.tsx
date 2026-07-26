export function Loading() { return <div className="state">Загрузка…</div>; }

export function Failure({ error }: { error: unknown }) {
  return <div className="state state-error">{error instanceof Error ? error.message : "Не удалось загрузить данные"}</div>;
}

export function Empty({ children = "Данных пока нет" }: { children?: React.ReactNode }) {
  return <div className="state">{children}</div>;
}
