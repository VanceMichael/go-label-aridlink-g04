import type { ApiError, ProgramOverview, Session, SiteList } from "./types";

export class ApiClient {
  private token = "";

  setToken(token: string) {
    this.token = token;
  }

  async login(email: string, password: string): Promise<Session> {
    return this.request<Session>("/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password })
    });
  }

  async logout(): Promise<void> {
    await this.request<void>("/v1/auth/logout", { method: "POST" });
    this.token = "";
  }

  overview(programId: string): Promise<ProgramOverview> {
    return this.request<ProgramOverview>(`/v1/programs/${encodeURIComponent(programId)}/overview`);
  }

  sites(programId: string): Promise<SiteList> {
    return this.request<SiteList>(`/v1/programs/${encodeURIComponent(programId)}/sites`);
  }

  async approveSite(siteId: string, version: number): Promise<void> {
    await this.request<void>(`/v1/sites/${encodeURIComponent(siteId)}/approve`, {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({ version })
    });
  }

  async startCampaign(campaignId: string, version: number): Promise<void> {
    await this.request<void>(`/v1/monitoring/campaigns/${encodeURIComponent(campaignId)}/start`, {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({ version })
    });
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body) headers.set("Content-Type", "application/json");
    if (this.token) headers.set("Authorization", `Bearer ${this.token}`);
    const response = await fetch(path, { ...init, headers });
    if (!response.ok) {
      const failure = (await response.json().catch(() => ({ error: { code: "network", message: response.statusText } }))) as ApiError;
      throw new Error(failure.error.message);
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }
}

export const api = new ApiClient();
