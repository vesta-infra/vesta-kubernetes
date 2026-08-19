import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'

type Phase = 'Running' | 'Pending' | 'Failed' | 'Degraded' | 'Sleeping'

const PHASE_ORDER: Phase[] = ['Running', 'Pending', 'Degraded', 'Failed', 'Sleeping']

// Status colours are reserved (never used as series hues) and always ship with a
// written label + count in the legend, so state never reads by colour alone.
const PHASE_TOKENS: Record<string, { fill: string; text: string; badge: string }> = {
  Running: { fill: 'bg-status-running', text: 'text-status-running', badge: 'bg-status-running-bg text-status-running border-status-running/20' },
  Pending: { fill: 'bg-status-pending', text: 'text-status-pending', badge: 'bg-status-pending-bg text-status-pending border-status-pending/20' },
  Degraded: { fill: 'bg-status-degraded', text: 'text-status-degraded', badge: 'bg-status-degraded-bg text-status-degraded border-status-degraded/20' },
  Failed: { fill: 'bg-status-failed', text: 'text-status-failed', badge: 'bg-status-failed-bg text-status-failed border-status-failed/20' },
  Sleeping: { fill: 'bg-status-sleeping', text: 'text-status-sleeping', badge: 'bg-status-sleeping-bg text-status-sleeping border-status-sleeping/20' },
}

export default function DashboardPage() {
  const { data: apps, isLoading: appsLoading } = useQuery({ queryKey: ['apps'], queryFn: () => api.listApps() })
  const { data: projects } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })
  const { data: activity, isLoading: activityLoading } = useQuery({
    queryKey: ['activity'],
    queryFn: () => api.getActivityFeed({ limit: 20 }),
    refetchInterval: 30000,
  })
  const { data: health } = useQuery({
    queryKey: ['healthDashboard'],
    queryFn: () => api.getHealthDashboard(),
    refetchInterval: 30000,
  })

  const appItems: any[] = apps?.items ?? []
  const healthApps: any[] = health?.apps ?? []
  const healthById = new Map(healthApps.map((a) => [a.id, a]))

  // Prefer the operator-backed summary; fall back to phases on the app list.
  const counts = PHASE_ORDER.reduce((acc, phase) => {
    acc[phase] = health?.summary?.[phase] ?? appItems.filter((a) => (a.status?.phase || 'Pending') === phase).length
    return acc
  }, {} as Record<string, number>)

  const totalApps = apps?.total ?? appItems.length
  const running = counts.Running ?? 0
  const attention = (counts.Failed ?? 0) + (counts.Degraded ?? 0)
  const tracked = PHASE_ORDER.reduce((sum, p) => sum + (counts[p] ?? 0), 0)

  const readyPods = healthApps.reduce((sum, a) => sum + (a.readyPods ?? 0), 0)
  const totalPods = healthApps.reduce((sum, a) => sum + (a.totalPods ?? 0), 0)
  const restarts = healthApps.reduce((sum, a) => sum + (a.restarts ?? 0), 0)

  const attentionApps = appItems
    .map((a) => ({ ...a, phase: a.status?.phase || healthById.get(a.id)?.phase || 'Pending' }))
    .filter((a) => a.phase === 'Failed' || a.phase === 'Degraded')

  return (
    <div className="space-y-6">
      <FleetOverview
        running={running}
        totalApps={totalApps}
        counts={counts}
        tracked={tracked}
        readyPods={readyPods}
        totalPods={totalPods}
        restarts={restarts}
        attention={attention}
        attentionApps={attentionApps}
      />

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <StatTile label="Projects" value={projects?.total ?? 0} hint="workspaces" href="/projects" delay={0} />
        <StatTile label="Applications" value={totalApps} hint="deployed services" href="/apps" delay={1} />
        <StatTile
          label="Needs attention"
          value={attention}
          hint={attention === 0 ? 'all clear' : 'failed or degraded'}
          href="/health"
          tone={attention > 0 ? 'critical' : totalApps > 0 ? 'good' : 'neutral'}
          delay={2}
        />
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-5 gap-4 items-start">
        <section className="xl:col-span-3 panel top-sheen overflow-hidden">
          <div className="panel-header">
            <div className="flex items-center gap-2.5">
              <h3 className="section-title">Applications</h3>
              <span className="chip tabular">{totalApps}</span>
            </div>
            {totalApps > 0 && (
              <Link to="/apps" className="text-[11px] font-mono uppercase tracking-wider text-text-tertiary hover:text-accent transition-colors">
                View all &rarr;
              </Link>
            )}
          </div>

          {appsLoading && <RowSkeletons rows={4} />}

          {!appsLoading && appItems.length === 0 && (
            <EmptyState
              title="No applications yet"
              body="Create a project, then deploy your first app from an image or a git push."
              action={{ to: '/projects', label: 'Create a project' }}
            />
          )}

          <div className="divide-y divide-border/50">
            {appItems.slice(0, 6).map((app, i) => (
              <AppRow key={app.id} app={app} health={healthById.get(app.id)} index={i} />
            ))}
          </div>

          {totalApps > 6 && (
            <Link
              to="/apps"
              className="flex items-center justify-center gap-1.5 px-5 py-3 border-t border-border/50 text-[11px] font-mono uppercase tracking-wider text-text-tertiary hover:text-accent hover:bg-surface-3/40 transition-colors"
            >
              {totalApps - 6} more &rarr;
            </Link>
          )}
        </section>

        <section className="xl:col-span-2 panel top-sheen overflow-hidden xl:sticky xl:top-24">
          <div className="panel-header">
            <h3 className="section-title">Activity</h3>
            <span className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-wider text-text-tertiary">
              <span className="relative flex w-1.5 h-1.5">
                <span className="absolute inset-0 rounded-full bg-status-running animate-ring-pulse" />
                <span className="relative w-1.5 h-1.5 rounded-full bg-status-running" />
              </span>
              live
            </span>
          </div>

          {activityLoading && <RowSkeletons rows={5} />}

          {!activityLoading && (!activity?.items || activity.items.length === 0) && (
            <EmptyState title="Nothing yet" body="Deploys, scales, and rollbacks show up here as they happen." />
          )}

          {activity?.items?.length ? (
            <div className="px-5 py-4">
              <ol className="relative">
                {/* Rail: one hairline behind every marker, ending at the last one. */}
                <span className="absolute left-[5px] top-2 bottom-4 w-px bg-border/70" aria-hidden="true" />
                {activity.items.slice(0, 8).map((entry: any) => (
                  <ActivityEntry key={entry.id} entry={entry} />
                ))}
              </ol>
            </div>
          ) : null}
        </section>
      </div>
    </div>
  )
}

/* ---------------------------------------------------------------- fleet hero */

function FleetOverview({
  running, totalApps, counts, tracked, readyPods, totalPods, restarts, attention, attentionApps,
}: {
  running: number; totalApps: number; counts: Record<string, number>; tracked: number
  readyPods: number; totalPods: number; restarts: number; attention: number; attentionApps: any[]
}) {
  const healthyPct = tracked > 0 ? Math.round((running / tracked) * 100) : 0
  const podPct = totalPods > 0 ? Math.round((readyPods / totalPods) * 100) : 0
  const segments = PHASE_ORDER.filter((p) => (counts[p] ?? 0) > 0)

  return (
    <section className="panel top-sheen relative overflow-hidden animate-slide-up">
      <div className="absolute inset-0 grid-fade pointer-events-none" aria-hidden="true" />
      <div className="absolute -top-24 -right-16 w-[420px] h-[280px] bg-gradient-radial from-accent/[0.07] via-transparent to-transparent pointer-events-none" aria-hidden="true" />

      <div className="relative grid grid-cols-1 lg:grid-cols-[1.15fr_1fr] gap-8 px-6 py-7">
        {/* Hero figure -- the one number the page leads with */}
        <div className="flex flex-col">
          <p className="kpi-label mb-3">Fleet status</p>
          <div className="flex items-end gap-3">
            <span className="text-[56px] leading-[0.9] font-semibold tracking-[-0.03em] text-text-primary">{running}</span>
            <span className="text-sm text-text-secondary pb-1.5">
              of <span className="text-text-primary font-medium tabular">{tracked || totalApps}</span> apps running
            </span>
          </div>

          <div className="mt-6">
            {/* Composition meter: 2px surface gaps do the separating, no strokes. */}
            {segments.length > 0 ? (
              <div className="flex items-center gap-[2px] h-2" role="img" aria-label={`Fleet composition: ${segments.map((p) => `${counts[p]} ${p}`).join(', ')}`}>
                {segments.map((phase) => (
                  <div
                    key={phase}
                    title={`${phase}: ${counts[phase]}`}
                    className={`h-full rounded-[2px] ${PHASE_TOKENS[phase].fill} transition-all duration-700 ease-out`}
                    style={{ width: `${((counts[phase] ?? 0) / tracked) * 100}%` }}
                  />
                ))}
              </div>
            ) : (
              <div className="meter-track" />
            )}

            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 mt-4">
              {PHASE_ORDER.map((phase) => (
                <span key={phase} className="flex items-center gap-2">
                  <span className={`w-1.5 h-1.5 rounded-full ${PHASE_TOKENS[phase].fill} ${(counts[phase] ?? 0) === 0 ? 'opacity-30' : ''}`} />
                  <span className={`text-[11px] ${(counts[phase] ?? 0) === 0 ? 'text-text-quaternary' : 'text-text-secondary'}`}>{phase}</span>
                  <span className={`text-[11px] font-mono tabular ${(counts[phase] ?? 0) === 0 ? 'text-text-quaternary' : 'text-text-primary'}`}>
                    {counts[phase] ?? 0}
                  </span>
                </span>
              ))}
            </div>
          </div>

          {/* Name the apps behind the attention count -- a number alone isn't actionable. */}
          <div className="mt-auto pt-6">
            {attentionApps.length > 0 ? (
              <div className="flex flex-wrap items-center gap-2">
                <span className="kpi-label">Needs attention</span>
                {attentionApps.slice(0, 3).map((app) => (
                  <Link
                    key={app.id}
                    to={`/apps/${app.id}`}
                    className="inline-flex items-center gap-1.5 text-[11px] font-mono px-2 py-1 rounded-md border bg-surface-2/60 border-border/70 text-text-secondary
                               hover:text-text-primary hover:border-border-hover transition-colors"
                  >
                    <span className={`w-1.5 h-1.5 rounded-full ${PHASE_TOKENS[app.phase]?.fill ?? 'bg-status-failed'}`} />
                    {app.name}
                  </Link>
                ))}
                {attentionApps.length > 3 && (
                  <Link to="/health" className="text-[11px] font-mono text-text-tertiary hover:text-accent transition-colors">
                    +{attentionApps.length - 3} more
                  </Link>
                )}
              </div>
            ) : (
              <p className="flex items-center gap-2 text-[11px] text-text-tertiary">
                <svg className="w-3.5 h-3.5 text-status-running" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                </svg>
                No applications need attention
              </p>
            )}
          </div>
        </div>

        {/* Supporting metrics */}
        <div className="grid grid-cols-2 gap-x-6 gap-y-6 lg:border-l lg:border-border/60 lg:pl-8">
          {/* With nothing deployed there is no ratio to judge, so these stay neutral
              rather than reporting a scary 0%. */}
          <Metric
            label="Healthy"
            value={tracked > 0 ? `${healthyPct}%` : '—'}
            meter={tracked > 0 ? healthyPct : undefined}
            tone={tracked === 0 ? 'muted' : healthyPct >= 90 ? 'good' : healthyPct >= 60 ? 'warning' : 'critical'}
          />
          <Metric
            label="Pods ready"
            value={totalPods > 0 ? `${readyPods}/${totalPods}` : '—'}
            meter={totalPods > 0 ? podPct : undefined}
            tone={totalPods === 0 ? 'muted' : podPct >= 90 ? 'good' : podPct >= 60 ? 'warning' : 'critical'}
          />
          <Metric label="Restarts" value={restarts} tone={restarts > 5 ? 'warning' : 'neutral'} />
          <Metric label="Attention" value={attention} tone={attention > 0 ? 'critical' : tracked > 0 ? 'good' : 'muted'} />
          <div className="col-span-2 flex items-center gap-2">
            <Link to="/health" className="btn-outline">Health dashboard</Link>
            <Link to="/apps" className="btn-outline">All apps</Link>
          </div>
        </div>
      </div>
    </section>
  )
}

const TONE_TEXT: Record<string, string> = {
  good: 'text-status-running',
  warning: 'text-status-pending',
  critical: 'text-status-failed',
  neutral: 'text-text-primary',
  muted: 'text-text-tertiary',
}
const TONE_FILL: Record<string, string> = {
  good: 'bg-status-running',
  warning: 'bg-status-pending',
  critical: 'bg-status-failed',
  neutral: 'bg-text-tertiary',
  muted: 'bg-surface-5',
}

function Metric({ label, value, meter, tone = 'neutral' }: { label: string; value: string | number; meter?: number; tone?: string }) {
  return (
    <div>
      <p className="kpi-label mb-2">{label}</p>
      <p className={`text-xl font-semibold tracking-[-0.01em] ${TONE_TEXT[tone]}`}>{value}</p>
      {meter !== undefined && (
        <div className="meter-track mt-2.5">
          <div className={`meter-fill ${TONE_FILL[tone]}`} style={{ width: `${Math.min(100, Math.max(0, meter))}%` }} />
        </div>
      )}
    </div>
  )
}

/* ---------------------------------------------------------------- stat tiles */

function StatTile({
  label, value, hint, href, tone = 'neutral', delay = 0,
}: { label: string; value: number; hint?: string; href?: string; tone?: string; delay?: number }) {
  const inner = (
    <div
      className={`card top-sheen px-5 py-5 h-full relative overflow-hidden animate-slide-up ${href ? 'transition-all duration-300 hover:border-border-hover hover:bg-surface-3/60 hover:shadow-card-hover group cursor-pointer' : ''}`}
      style={{ animationDelay: `${delay * 0.06}s` }}
    >
      <div className="flex items-start justify-between">
        <p className="kpi-label">{label}</p>
        {href && (
          <svg className="w-3.5 h-3.5 text-text-quaternary group-hover:text-accent group-hover:translate-x-0.5 transition-all duration-300" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
          </svg>
        )}
      </div>
      <p className={`kpi-value mt-3.5 ${tone === 'neutral' ? '' : TONE_TEXT[tone]}`}>{value}</p>
      {hint && <p className="text-[11px] text-text-tertiary mt-2">{hint}</p>}
    </div>
  )
  return href ? <Link to={href} className="block h-full">{inner}</Link> : inner
}

/* ----------------------------------------------------------------- app rows */

function AppRow({ app, health, index }: { app: any; health?: any; index: number }) {
  const phase = (app.status?.phase || health?.phase || 'Pending') as string
  const ready = health?.readyPods
  const total = health?.totalPods
  const restarts = health?.restarts ?? 0

  return (
    <Link
      to={`/apps/${app.id}`}
      className="flex items-center gap-4 px-5 py-3.5 group transition-colors duration-200 hover:bg-surface-3/50 animate-slide-up"
      style={{ animationDelay: `${index * 0.03}s` }}
    >
      <div className="w-8 h-8 rounded-lg bg-surface-3 border border-border/80 flex items-center justify-center text-[11px] font-mono font-semibold text-text-secondary group-hover:text-accent group-hover:border-accent/25 group-hover:bg-accent/[0.06] transition-all duration-300 shrink-0">
        {app.name?.charAt(0)?.toUpperCase() || 'A'}
      </div>

      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-semibold text-text-primary truncate group-hover:text-accent transition-colors duration-200">
          {app.name}
        </p>
        <div className="flex items-center gap-2 mt-1">
          <span className="text-[11px] font-mono text-text-tertiary truncate">{app.projectName || app.project || health?.project || '—'}</span>
          {health?.sleepMode && <span className="chip">sleep</span>}
        </div>
      </div>

      {/* Both metric slots keep their width whether or not they have a value, so
          the rows read as columns instead of drifting. */}
      <div className="hidden sm:flex flex-col items-end gap-1.5 w-14 shrink-0">
        {total > 0 ? (
          <>
            <span className="text-[11px] font-mono tabular text-text-secondary">{ready}/{total}</span>
            <div className="meter-track h-1">
              <div
                className={`meter-fill ${ready === total ? 'bg-status-running' : ready === 0 ? 'bg-status-failed' : 'bg-status-pending'}`}
                style={{ width: `${(ready / total) * 100}%` }}
              />
            </div>
          </>
        ) : null}
      </div>

      <span
        className={`hidden md:inline-flex items-center justify-end gap-1 w-9 text-[11px] font-mono tabular shrink-0 ${restarts > 5 ? 'text-status-degraded' : 'text-text-tertiary'}`}
        title={`${restarts} container restarts`}
      >
        {restarts > 0 ? (
          <>
            <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M4 4v5h5M20 20v-5h-5M20 9A8 8 0 006.3 5.7M4 15a8 8 0 0013.7 3.3" />
            </svg>
            {restarts}
          </>
        ) : null}
      </span>

      <StatusBadge phase={phase} />
    </Link>
  )
}

function StatusBadge({ phase }: { phase?: string }) {
  const p = phase && PHASE_TOKENS[phase] ? phase : 'Pending'
  const tokens = PHASE_TOKENS[p]
  return (
    <span className={`status-badge border shrink-0 w-[86px] justify-center ${tokens.badge}`}>
      <span className={`inline-block w-1.5 h-1.5 rounded-full bg-current ${p === 'Running' ? 'animate-glow-pulse' : ''}`} />
      {p}
    </span>
  )
}

/* ------------------------------------------------------------ activity rail */

// Each action maps to a status tone plus a glyph, so the entry type is never
// carried by colour alone.
const ACTION_META: Record<string, { tone: string; icon: string }> = {
  'app.created': { tone: 'good', icon: 'plus' },
  'app.updated': { tone: 'accent', icon: 'pencil' },
  'app.deleted': { tone: 'critical', icon: 'trash' },
  'app.deployed': { tone: 'good', icon: 'rocket' },
  'app.redeployed': { tone: 'accent', icon: 'rocket' },
  'app.rolled_back': { tone: 'warning', icon: 'undo' },
  'app.restarted': { tone: 'accent', icon: 'undo' },
  'app.scaled': { tone: 'accent', icon: 'scale' },
  'app.cloned': { tone: 'good', icon: 'copy' },
  'project.created': { tone: 'good', icon: 'plus' },
  'project.updated': { tone: 'accent', icon: 'pencil' },
  'project.deleted': { tone: 'critical', icon: 'trash' },
  'environment.created': { tone: 'good', icon: 'plus' },
  'environment.deleted': { tone: 'critical', icon: 'trash' },
  'environment.cloned': { tone: 'good', icon: 'copy' },
  'secret.created': { tone: 'accent', icon: 'key' },
}

const ICON_PATHS: Record<string, string> = {
  plus: 'M12 5v14M5 12h14',
  pencil: 'M16.5 3.5l4 4L7 21H3v-4L16.5 3.5z',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 14h10l1-14',
  rocket: 'M5 19l3-3m6.5-11.5a8 8 0 01-9 12.5l-1.5 1.5m10.5-14a8 8 0 01-12.5 9L4 20',
  undo: 'M4 4v5h5M20 9A8 8 0 006.3 5.7',
  scale: 'M4 18V8m6 10V4m6 14v-7m4 7V9',
  copy: 'M8 8h10v12H8zM6 16H4V4h10v2',
  key: 'M15 7a4 4 0 11-3.2 6.4L4 21H3v-3l7.6-7.8A4 4 0 0115 7z',
}

const ACTION_TONE_TEXT: Record<string, string> = {
  good: 'text-status-running',
  warning: 'text-status-pending',
  critical: 'text-status-failed',
  accent: 'text-accent',
  neutral: 'text-text-secondary',
}
const ACTION_TONE_BG: Record<string, string> = {
  good: 'bg-status-running-bg border-status-running/20',
  warning: 'bg-status-pending-bg border-status-pending/20',
  critical: 'bg-status-failed-bg border-status-failed/20',
  accent: 'bg-accent/10 border-accent/20',
  neutral: 'bg-surface-3 border-border',
}

function formatTimeAgo(dateStr?: string): string {
  if (!dateStr) return ''
  const t = new Date(dateStr).getTime()
  if (Number.isNaN(t)) return ''
  const mins = Math.floor((Date.now() - t) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

function ActivityEntry({ entry }: { entry: any }) {
  const meta = ACTION_META[entry.action] || { tone: 'neutral', icon: 'pencil' }
  const [group, verb] = (entry.action || 'unknown').split('.')

  return (
    <li className="relative flex gap-3.5 pb-4 last:pb-0 group">
      <span
        className={`relative z-10 mt-0.5 w-[11px] h-[11px] rounded-full border shrink-0 ring-4 ring-surface-1 ${ACTION_TONE_BG[meta.tone]}`}
        aria-hidden="true"
      />
      <div className="min-w-0 flex-1 -mt-1">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-[12px] leading-5">
              <span className={`inline-flex items-center gap-1.5 font-mono ${ACTION_TONE_TEXT[meta.tone]}`}>
                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                  <path strokeLinecap="round" strokeLinejoin="round" d={ICON_PATHS[meta.icon]} />
                </svg>
                {(verb || group || '').replace(/_/g, ' ')}
              </span>
              {entry.resourceName && (
                <span className="text-text-primary font-medium ml-1.5 break-words">{entry.resourceName}</span>
              )}
            </p>
            <div className="flex items-center gap-2 mt-1">
              {entry.username && <span className="text-[10px] text-text-tertiary">{entry.username}</span>}
              {entry.username && <span className="text-[10px] text-text-quaternary">&middot;</span>}
              <span className="text-[10px] font-mono uppercase tracking-wider text-text-quaternary">{group}</span>
              {entry.environment && <span className="chip">{entry.environment}</span>}
            </div>
          </div>
          <span className="text-[10px] font-mono text-text-tertiary whitespace-nowrap pt-0.5">{formatTimeAgo(entry.createdAt)}</span>
        </div>
      </div>
    </li>
  )
}

/* ------------------------------------------------------- shared small parts */

function RowSkeletons({ rows }: { rows: number }) {
  return (
    <div className="divide-y divide-border/40">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 px-5 py-4">
          <div className="w-8 h-8 rounded-lg bg-surface-3/80 animate-pulse" style={{ animationDelay: `${i * 0.1}s` }} />
          <div className="flex-1 space-y-2">
            <div className="h-2.5 w-1/3 rounded bg-surface-3/80 animate-pulse" style={{ animationDelay: `${i * 0.1}s` }} />
            <div className="h-2 w-1/5 rounded bg-surface-3/50 animate-pulse" style={{ animationDelay: `${i * 0.1 + 0.05}s` }} />
          </div>
          <div className="h-5 w-16 rounded-md bg-surface-3/60 animate-pulse" style={{ animationDelay: `${i * 0.1}s` }} />
        </div>
      ))}
    </div>
  )
}

function EmptyState({ title, body, action }: { title: string; body: string; action?: { to: string; label: string } }) {
  return (
    <div className="px-6 py-14 text-center">
      <div className="w-11 h-11 rounded-xl bg-surface-3/70 border border-border flex items-center justify-center mx-auto mb-4">
        <svg className="w-[18px] h-[18px] text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
        </svg>
      </div>
      <p className="text-[13px] font-semibold text-text-primary">{title}</p>
      <p className="text-xs text-text-tertiary mt-1.5 max-w-sm mx-auto leading-relaxed">{body}</p>
      {action && (
        <Link to={action.to} className="btn-outline inline-flex mt-5">{action.label}</Link>
      )}
    </div>
  )
}
