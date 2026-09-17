import { useEffect, useSyncExternalStore } from 'react'
import type { components } from './schema'

export type Session = components['schemas']['Session']

/** Why a session ended, for the sign-in page to explain. */
export type EndReason = 'signed_out' | 'expired' | 'refused'

// The token lives in sessionStorage (ADR 0009): it survives a reload and ends
// with the tab.
const storageKey = 'loghub.session'

type Listener = () => void
const listeners = new Set<Listener>()
let current: Session | null = load()
let ended: EndReason | null = null

function load(): Session | null {
  try {
    const stored = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as Session | null
    return stored && isLive(stored) ? stored : null
  } catch {
    return null
  }
}

function isLive(s: Session): boolean {
  return typeof s.token === 'string' && Date.parse(s.expires_at) > Date.now()
}

function end(reason: EndReason) {
  if (!current) return
  current = null
  ended = reason
  try {
    sessionStorage.removeItem(storageKey)
  } catch {
    // Storage is unavailable; the session was only ever in memory.
  }
  listeners.forEach((l) => l())
}

export const session = {
  /** The session requests are sent with, or null if there is none or it has expired. */
  get(): Session | null {
    if (current && !isLive(current)) end('expired')
    return current
  },
  /** The session as last stored, without checking its expiry. Safe to call while rendering. */
  snapshot(): Session | null {
    return current
  },
  start(s: Session) {
    current = s
    ended = null
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(s))
    } catch {
      // Storage is unavailable, so the session lasts until the page reloads.
    }
    listeners.forEach((l) => l())
  },
  end,
  /** Why the last session in this tab ended, if one did. */
  endedBecause(): EndReason | null {
    return ended
  },
  subscribe(l: Listener): () => void {
    listeners.add(l)
    return () => listeners.delete(l)
  },
}

// setTimeout fires at once for delays past this.
const maxDelay = 2 ** 31 - 1

/** The current session. It ends when its token expires, even if no request is made. */
export function useSession(): Session | null {
  const s = useSyncExternalStore(session.subscribe, session.snapshot)
  useEffect(() => {
    if (!s) return
    const delay = Math.min(Math.max(Date.parse(s.expires_at) - Date.now(), 0), maxDelay)
    const timer = setTimeout(() => session.get(), delay)
    return () => clearTimeout(timer)
  }, [s])
  return s
}
