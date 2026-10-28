import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api-client";
import { formatMoneyWhole } from "../../lib/money";

export interface Stage { id: string; name: string; sort_order: number; kind: string }
export interface Opp {
  id: string; title: string; party_id?: string; customer_name?: string; company_name?: string; contact_name?: string; contact_email?: string; contact_phone?: string;
  owner_user_id?: string; created_at?: string; value: string; currency: string; stage_id: string; stage_kind: string; expected_close?: string; source?: string; notes?: string; open_tasks: number;
}
export interface Activity { id: string; kind: string; subject: string; notes?: string; due_at?: string; done_at?: string; created_at: string; opportunity_title?: string; opportunity_id: string }
export interface Owner { id: string; name: string }

export const KIND_LABEL: Record<string, string> = { CALL: "Call", EMAIL: "Email", MEETING: "Meeting", NOTE: "Note", TASK: "Task" };

export const oppKeys = { stages: ["crm", "stages"], opps: ["crm", "opps"], tasks: ["crm", "tasks"], owners: ["crm", "owners"] } as const;

/** The party an opportunity is about: the linked customer, else the company, else the contact. */
export function who(o: Opp): string {
  return o.customer_name || o.company_name || o.contact_name || "";
}

/** Every fetch the page makes, with its own retry so one failing endpoint never leaves a skeleton up forever. */
export function useCrmData() {
  const stages = useQuery({ queryKey: oppKeys.stages, queryFn: () => api.getListField<Stage>("/crm/stages", "stages") });
  const opps = useQuery({ queryKey: oppKeys.opps, queryFn: () => api.getListField<Opp>("/crm/opportunities", "opportunities") });
  const tasks = useQuery({ queryKey: oppKeys.tasks, queryFn: () => api.getListField<Activity>("/crm/tasks", "tasks") });
  const owners = useQuery({ queryKey: oppKeys.owners, queryFn: () => api.getListField<Owner>("/crm/owners", "owners") });
  return { stages, opps, tasks, owners };
}

/** Moves a deal between stages. The card jumps immediately and snaps back if the server refuses. */
export function useMoveOpportunity(stages: Stage[] | undefined) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, stageId }: { id: string; stageId: string }) => api.post(`/crm/opportunities/${id}/move`, { stage_id: stageId }),
    onMutate: async ({ id, stageId }) => {
      await qc.cancelQueries({ queryKey: oppKeys.opps });
      const previous = qc.getQueryData<Opp[]>(oppKeys.opps);
      const kind = stages?.find((s) => s.id === stageId)?.kind;
      qc.setQueryData<Opp[]>(oppKeys.opps, (old) => old?.map((o) => (o.id === id ? { ...o, stage_id: stageId, stage_kind: kind ?? o.stage_kind } : o)));
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(oppKeys.opps, ctx.previous);
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: ["crm"] }),
  });
}

/** Sums deal values per currency: values keep their own currency, so a total is never mixed across them. */
export function totalsByCurrency(opps: Opp[]): { currency: string; amount: number }[] {
  const sums = new Map<string, number>();
  for (const o of opps) sums.set(o.currency, (sums.get(o.currency) ?? 0) + (Number(o.value) || 0));
  return [...sums.entries()].map(([currency, amount]) => ({ currency, amount })).sort((a, b) => b.amount - a.amount);
}

export function formatTotals(opps: Opp[]): string {
  const totals = totalsByCurrency(opps);
  if (totals.length === 0) return "—";
  return totals.map((t) => formatMoneyWhole({ amount: String(t.amount), currency: t.currency })).join(" + ");
}

/** A phone number reduced to digits for a wa.me link. */
export function whatsappDigits(phone: string): string {
  return phone.replace(/\D/g, "");
}

export function matches(o: Opp, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [o.title, o.customer_name, o.company_name, o.contact_name, o.contact_email, o.contact_phone, o.source].some((v) => v?.toLowerCase().includes(q));
}

export { whenLabel } from "./crmLogic";
export type { Band, LeadScore, ScoreFactor } from "./crmLogic";
export { LOSS_REASONS, csvCell, downloadCsv, leadScore, leadsToCsv, stageProbability } from "./crmLogic";
