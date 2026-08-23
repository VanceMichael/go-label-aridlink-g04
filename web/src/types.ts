export type User = {
  id: string;
  organization_id: string;
  email: string;
  role: "platform_admin" | "program_manager" | "field_officer" | "technical_reviewer" | "finance_reviewer";
};

export type Session = {
  token: string;
  expires_at: string;
  user: User;
};

export type ProgramOverview = {
  program_id: string;
  program_name: string;
  program_status: string;
  budget_cents: number;
  held_budget_cents: number;
  disbursed_cents: number;
  site_states: Record<string, number>;
  campaign_states: Record<string, number>;
  work_order_states: Record<string, number>;
  evidence_states: Record<string, number>;
  milestone_states: Record<string, number>;
  published_alert_count: number;
  pending_review_count: number;
  generated_at: string;
};

export type Site = {
  id: string;
  program_id: string;
  organization_id: string;
  name: string;
  country_code: string;
  area_hectares: number;
  ecosystem: string;
  status: string;
  version: number;
};

export type SiteList = { items: Site[] };

export type ApiError = { error: { code: string; message: string; request_id?: string } };
