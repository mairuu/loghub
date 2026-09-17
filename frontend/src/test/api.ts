// A stand-in for the API. The client takes fetch when it is created, so
// setup.ts installs fakeFetch before any test imports it.

type Handler = (req: Request) => Response | Promise<Response>

let handlers: Record<string, Handler> = {}

/** Every request the fake API received, in order. */
export const requests: Request[] = []

/** Answers requests by "METHOD /path"; anything else gets Go's plain-text 404. */
export function mockApi(routes: Record<string, Handler>) {
  handlers = routes
  requests.length = 0
}

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function apiError(status: number, code: string, message: string, extra: object = {}): Response {
  return json({ code, message, request_id: 'req-1', ...extra }, status)
}

export async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const req = new Request(input, init)
  requests.push(req.clone())
  const handler = handlers[`${req.method} ${new URL(req.url).pathname}`]
  return handler ? handler(req) : new Response('404 page not found\n', { status: 404, headers: { 'Content-Type': 'text/plain' } })
}

export const adminSession = { token: 'admin-token', expires_at: '2999-01-01T00:00:00Z', role: 'admin' } as const
export const viewerSession = { token: 'viewer-token', expires_at: '2999-01-01T00:00:00Z', role: 'viewer', tenant: 'demoA' } as const

export const tenants = {
  items: [
    { id: 'demoA', name: 'Demo A' },
    { id: 'demoB', name: 'Demo B' },
  ],
}
