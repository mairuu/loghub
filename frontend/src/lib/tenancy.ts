import type { Session } from '../api/session'

/**
 * Whether tenant is one the session can't read. Only a viewer has such
 * tenants, and the API would refuse each request with tenant_not_permitted,
 * so pages say so once instead.
 */
export function isForeignTenant(s: Session | null, tenant: string): boolean {
  return s?.role === 'viewer' && tenant !== '' && tenant !== s.tenant
}
