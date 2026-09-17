import { useSession } from '../api/session'
import { Notice } from './ui'

/** Why a viewer sees nothing for another tenant, with the way back to their own. */
export function ForeignTenant({ tenant, onShowOwn }: { tenant: string; onShowOwn: () => void }) {
  const s = useSession()
  return (
    <Notice tone="warn">
      This link asks for tenant {tenant}, and you can only see your own, {s?.tenant}.{' '}
      <button type="button" onClick={onShowOwn} className="font-medium underline">
        Show {s?.tenant}
      </button>
    </Notice>
  )
}
