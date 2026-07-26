"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

const eventNames = ["project.updated", "plan.updated", "plan_run.updated", "run.updated", "task.updated", "approval.updated", "onboarding.updated", "work_item.updated"];
const eventsURL = process.env.NEXT_PUBLIC_ORCHESTRATOR_EVENTS_URL ?? "http://127.0.0.1:8080/api/v1/events";

export function LiveUpdates() {
  const client = useQueryClient();
  useEffect(() => {
    const source = new EventSource(eventsURL);
    const refresh = () => client.invalidateQueries();
    eventNames.forEach((name) => source.addEventListener(name, refresh));
    source.onerror = () => { /* EventSource reconnects; query polling remains active. */ };
    return () => {
      eventNames.forEach((name) => source.removeEventListener(name, refresh));
      source.close();
    };
  }, [client]);
  return null;
}
