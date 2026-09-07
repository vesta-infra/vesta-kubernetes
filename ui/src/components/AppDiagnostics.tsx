import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

// A "Failed" phase on its own is a dead end -- it says something is wrong but not
// what. This panel answers the question: which pod, which container, what went
// wrong, and what to do about it.

const SEVERITY_STYLES: Record<string, { dot: string; text: string; ring: string }> = {
  error: { dot: 'bg-status-failed', text: 'text-status-failed', ring: 'border-status-failed/20 bg-status-failed/[0.04]' },
  warning: { dot: 'bg-status-pending', text: 'text-status-pending', ring: 'border-status-pending/20 bg-status-pending/[0.04]' },
  info: { dot: 'bg-text-tertiary', text: 'text-text-secondary', ring: 'border-border bg-surface-2/40' },
}

function timeAgo(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const mins = Math.floor((Date.now() - t) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

export default function AppDiagnostics({ appId, phase }: { appId: string; phase?: string }) {
  const healthy = phase === 'Running' || phase === 'Sleeping' || phase === 'Stopped'

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['appDiagnostics', appId],
    queryFn: () => api.getAppDiagnostics(appId),
    // Only poll while something is wrong; a healthy app doesn't need the traffic.
    refetchInterval: healthy ? false : 20000,
    enabled: !healthy,
  })

  if (healthy) return null

  const environments: any[] = data?.environments ?? []
  const allIssues = environments.flatMap((env: any) =>
    (env.issues ?? []).map((issue: any) => ({ ...issue, environment: env.environment })),
  )
  const hasFindings = allIssues.length > 0 || environments.some((e: any) => (e.events ?? []).length > 0)

  return (
    <section className="card top-sheen p-5 border-status-failed/20">
      <div className="flex items-start justify-between gap-4 mb-4">
        <div className="min-w-0">
          <h3 className="section-title">Why it's not running</h3>
          {data?.reason && (
            <p className="mt-2 flex items-center gap-2">
              <span className="status-badge border bg-status-failed-bg text-status-failed border-status-failed/20">
                {data.reason}
              </span>
            </p>
          )}
          {data?.message && (
            <p className="text-[13px] text-text-primary leading-relaxed mt-2.5">{data.message}</p>
          )}
          {data?.hint && (
            <p className="text-xs text-text-secondary leading-relaxed mt-2">{data.hint}</p>
          )}
        </div>
        <button onClick={() => refetch()} disabled={isFetching} className="btn-outline shrink-0">
          {isFetching ? 'Checking...' : 'Re-check'}
        </button>
      </div>

      {isLoading && <p className="text-xs text-text-tertiary">Gathering diagnostics...</p>}

      {!isLoading && !hasFindings && !data?.message && (
        <p className="text-xs text-text-tertiary leading-relaxed">
          No specific cause found yet. The app may still be starting -- check the Logs tab
          if this persists.
        </p>
      )}

      <div className="space-y-5">
        {environments.map((env: any) => {
          const issues = env.issues ?? []
          const events = env.events ?? []
          if (issues.length === 0 && events.length === 0) return null

          return (
            <div key={env.environment}>
              <div className="flex items-center gap-2 mb-2.5">
                <span className="text-[11px] font-mono uppercase tracking-wider text-text-secondary">{env.environment}</span>
                <span className="chip tabular">{env.readyPods}/{env.totalPods} pods ready</span>
              </div>

              <div className="space-y-2">
                {issues.map((issue: any, i: number) => {
                  const style = SEVERITY_STYLES[issue.severity] || SEVERITY_STYLES.info
                  return (
                    <div key={`${issue.pod}-${issue.container}-${i}`} className={`rounded-lg border px-3.5 py-3 ${style.ring}`}>
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className={`w-1.5 h-1.5 rounded-full ${style.dot}`} />
                        <span className={`text-xs font-mono font-medium ${style.text}`}>{issue.reason}</span>
                        {issue.pod && (
                          <span className="text-[11px] font-mono text-text-tertiary">
                            {issue.pod}{issue.container ? ` · ${issue.container}` : ''}
                          </span>
                        )}
                        {issue.restarts > 0 && (
                          <span className="chip tabular">{issue.restarts} restart{issue.restarts !== 1 ? 's' : ''}</span>
                        )}
                      </div>
                      {issue.message && (
                        <p className="text-xs text-text-primary leading-relaxed mt-1.5 break-words">{issue.message}</p>
                      )}
                      {issue.hint && (
                        <p className="text-[11px] text-text-secondary leading-relaxed mt-1.5">{issue.hint}</p>
                      )}
                    </div>
                  )
                })}
              </div>

              {events.length > 0 && (
                <div className="mt-3">
                  <p className="text-[10px] font-mono uppercase tracking-[0.16em] text-text-quaternary mb-2">
                    Recent cluster warnings
                  </p>
                  <div className="space-y-1">
                    {events.slice(0, 5).map((event: any, i: number) => (
                      <div key={`${event.object}-${i}`} className="flex items-start gap-2.5 text-[11px]">
                        <span className="font-mono text-status-pending shrink-0">{event.reason}</span>
                        <span className="text-text-secondary min-w-0 flex-1 break-words">{event.message}</span>
                        {event.count > 1 && <span className="chip tabular shrink-0">×{event.count}</span>}
                        <span className="text-text-quaternary whitespace-nowrap shrink-0">{timeAgo(event.lastSeen)}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </section>
  )
}
