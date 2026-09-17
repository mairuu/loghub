import createClient, { type Middleware } from 'openapi-fetch'
import type { components, paths } from './schema'
import { session } from './session'

export type Schemas = components['schemas']

/** An answer from the API other than success, as its error envelope describes it. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId?: string
  readonly fieldErrors: Schemas['FieldError'][]

  constructor(status: number, body: Partial<Schemas['ErrorResponse']> | undefined, fallback: string) {
    super(body?.message || fallback)
    this.name = 'ApiError'
    this.status = status
    this.code = body?.code ?? 'unexpected_response'
    this.requestId = body?.request_id
    this.fieldErrors = body?.errors ?? []
  }
}

const authenticate: Middleware = {
  onRequest({ request }) {
    const s = session.get()
    if (s) request.headers.set('Authorization', `Bearer ${s.token}`)
    return request
  },
  onResponse({ response }) {
    // The token was refused before it should have expired, usually because
    // AUTH_SECRET changed. Either way it is no use any more. Signing in is
    // the only request sent without one, and there is no session to end then.
    if (response.status === 401) session.end('refused')
    return response
  },
}

// The API is on the UI's own origin, behind Caddy or Vite's proxy.
export const api = createClient<paths>({ baseUrl: globalThis.location?.origin ?? '' })
api.use(authenticate)

interface Result<T> {
  data?: T
  error?: unknown
  response: Response
}

/** The body of a successful response, or an ApiError for any other. */
export async function unwrap<T>(pending: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await pending
  if (!response.ok) {
    // Unrouted paths get a plain-text answer from Go's mux, not the envelope.
    const body = typeof error === 'object' && error !== null ? (error as Partial<Schemas['ErrorResponse']>) : undefined
    throw new ApiError(response.status, body, `The server answered ${response.status} ${response.statusText}`.trim())
  }
  return data as T
}
