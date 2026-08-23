import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./api";
import type { Session } from "./types";

const session: Session = {
  token: "session-token",
  expires_at: "2026-08-23T06:00:00Z",
  user: {
    id: "user-1",
    organization_id: "org-1",
    email: "manager@aridlink.test",
    role: "program_manager"
  }
};

afterEach(() => {
	vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ApiClient", () => {
  it("logs in with JSON credentials and no stale authorization", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(session));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient();

    await expect(client.login("manager@aridlink.test", "correct-horse-battery")).resolves.toEqual(session);

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/v1/auth/login");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      email: "manager@aridlink.test",
      password: "correct-horse-battery"
    });
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
    expect(new Headers(init.headers).get("Content-Type")).toBe("application/json");
  });

  it("encodes path parameters and sends the active bearer token", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient();
    client.setToken("active-token");

    await client.sites("program/with spaces");

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/v1/programs/program%2Fwith%20spaces/sites");
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer active-token");
    expect(new Headers(init.headers).get("Accept")).toBe("application/json");
  });

  it("adds an idempotency key to mutation requests", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(crypto, "randomUUID").mockReturnValue("00000000-0000-4000-8000-000000000042");
    const client = new ApiClient();
    client.setToken("active-token");

    await client.approveSite("site/9", 4);

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = new Headers(init.headers);
    expect(path).toBe("/v1/sites/site%2F9/approve");
    expect(headers.get("Idempotency-Key")).toBe("00000000-0000-4000-8000-000000000042");
    expect(JSON.parse(String(init.body))).toEqual({ version: 4 });
  });

  it("treats no-content responses as successful undefined values", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    const client = new ApiClient();
    client.setToken("active-token");

    await expect(client.logout()).resolves.toBeUndefined();
  });

  it("surfaces the backend error message", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({
      error: { code: "invalid_state", message: "program is not active", request_id: "req-9" }
    }, 422)));
    const client = new ApiClient();

    await expect(client.overview("program-1")).rejects.toThrow("program is not active");
  });

  it("falls back to HTTP status text when an error body is not JSON", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("upstream unavailable", {
      status: 502,
      statusText: "Bad Gateway",
      headers: { "Content-Type": "text/plain" }
    })));
    const client = new ApiClient();

    await expect(client.overview("program-1")).rejects.toThrow("Bad Gateway");
  });
});

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" }
  });
}
