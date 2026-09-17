import { useCallback, useSyncExternalStore } from 'react'
import { useAlerts } from './queries'
import { session } from './session'

// When the newest alert was last seen on the alerts page, per tenant scope,
// so the navigation can point out alerts raised since. Until the page has
// been visited in this browser, any alert is new.
const listeners = new Set<() => void>()

function storageKey(): string {
  return `loghub.alertsSeen.${session.snapshot()?.tenant ?? '*'}`
}

function readSeen(): string {
  try {
    return localStorage.getItem(storageKey()) ?? ''
  } catch {
    return ''
  }
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

/** Whether an alert newer than the last one seen exists, and a way to mark them all seen. */
export function useUnseenAlerts(): { unseen: boolean; markSeen: (newest: string) => void } {
  const seen = useSyncExternalStore(subscribe, readSeen)
  const newest = useAlerts('', 1).data?.items[0]?.created_at
  const markSeen = useCallback((t: string) => {
    try {
      if (Date.parse(t) > Date.parse(readSeen() || '0')) localStorage.setItem(storageKey(), t)
    } catch {
      return
    }
    listeners.forEach((l) => l())
  }, [])
  return { unseen: newest !== undefined && (!seen || Date.parse(newest) > Date.parse(seen)), markSeen }
}
