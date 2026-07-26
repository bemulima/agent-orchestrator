import type { Metadata } from "next";
import Link from "next/link";
import { Providers } from "@/components/providers";
import "@/app/globals.css";

export const metadata: Metadata = {
  title: "Course Orchestrator",
  description: "Owner interface for course-dev-orchestrator",
};

const navigation = [
  ["/", "Обзор"],
  ["/projects", "Проекты"],
  ["/plans", "Планы"],
  ["/runs", "Выполнение"],
  ["/approvals", "Согласования"],
] as const;

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru">
      <body>
        <Providers>
          <div className="app-shell">
            <aside className="sidebar">
              <Link className="brand" href="/">Course Orchestrator</Link>
              <nav aria-label="Основная навигация">
                {navigation.map(([href, label]) => <Link key={href} href={href}>{label}</Link>)}
              </nav>
              <a className="secondary-link" href="http://localhost:8233" target="_blank" rel="noreferrer">Temporal UI ↗</a>
            </aside>
            <main>{children}</main>
          </div>
        </Providers>
      </body>
    </html>
  );
}
