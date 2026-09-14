import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Cost, type CostEntry, type CostReport } from '../lib/api'

/**
 * What a project costs to run.
 *
 * Two figures, kept deliberately apart: what was measured over the chosen window, and what a
 * month would cost if nothing changed. They answer different questions, and presenting an
 * extrapolation as a measurement is how a cost page loses trust.
 *
 * The rates are always shown. There is no billing API to ask and Kubernetes has no notion of
 * a price, so every number here is reserved-resources times a rate card -- and it is only as
 * accurate as that card. Saying so is the difference between an estimate people can correct
 * and one they quietly stop believing.
 */

const WINDOWS = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
  { value: '90d', label: '90 days' },
]

function money(c: Cost | undefined, currency: string) {
  const value = c?.total ?? 0
  const symbol = currency === 'USD' ? '$' : ''
  // Sub-cent figures are common on a short window for a small app, and rounding them to
  // "$0.00" reads as "free" rather than "very cheap".
  const digits = value > 0 && value < 1 ? 4 : 2
  return `${symbol}${value.toFixed(digits)}${symbol ? '' : ` ${currency}`}`
}

function percent(v: number | undefined) {
  return v === undefined ? null : `${Math.round(v * 100)}%`
}

/** Efficiency is a finding, not a score: only a genuinely low number earns attention. */
function efficiencyTone(v: number | undefined) {
  if (v === undefined) return 'text-text-tertiary'
  if (v < 0.15) return 'text-status-degraded'
  if (v < 0.4) return 'text-text-secondary'
  return 'text-text-tertiary'
}

function Bar({ entry, max, currency }: { entry: CostEntry; max: number; currency: string }) {
  const width = max > 0 ? Math.max(1, (entry.cost.total / max) * 100) : 0

  // The three components of the bar are the three lines of the rate card, so the shape of
  // the bar says where the money actually goes -- a storage-heavy app looks different from a
  // CPU-heavy one at a glance, which a single total cannot show.
  const parts = [
    { key: 'cpu', value: entry.cost.cpu, className: 'bg-accent' },
    { key: 'memory', value: entry.cost.memory, className: 'bg-accent/55' },
    { key: 'storage', value: entry.cost.storage, className: 'bg-accent/25' },
  ]

  return (
    <div className="flex items-center gap-3 py-2">
      <div className="w-40 shrink-0 min-w-0">
        <div className="text-xs text-text-primary truncate font-mono">{entry.key}</div>
        {entry.environment && (
          <div className="text-[10px] text-text-tertiary">{entry.environment}</div>
        )}
      </div>

      <div className="flex-1 min-w-0">
        <div className="h-2 rounded-sm bg-surface-3 overflow-hidden flex" style={{ width: `${width}%` }}>
          {parts.map(p => (
            <div
              key={p.key}
              className={p.className}
              style={{ width: `${entry.cost.total > 0 ? (p.value / entry.cost.total) * 100 : 0}%` }}
              title={`${p.key}: ${money({ ...entry.cost, total: p.value }, currency)}`}
            />
          ))}
        </div>
      </div>

      <div className="w-24 shrink-0 text-right">
        <div className="text-xs font-mono text-text-primary">{money(entry.cost, currency)}</div>
        <div className="text-[10px] text-text-tertiary">{money(entry.projectedMonthly, currency)}/mo</div>
      </div>

      <div className="w-20 shrink-0 text-right">
        {entry.cpuEfficiency === undefined && entry.memoryEfficiency === undefined ? (
          <span className="text-[10px] text-text-tertiary">&mdash;</span>
        ) : (
          <div className="text-[10px] font-mono">
            <span className={efficiencyTone(entry.cpuEfficiency)}>
              {percent(entry.cpuEfficiency) ?? '—'} cpu
            </span>
            <span className="text-text-tertiary"> / </span>
            <span className={efficiencyTone(entry.memoryEfficiency)}>
              {percent(entry.memoryEfficiency) ?? '—'} mem
            </span>
          </div>
        )}
      </div>
    </div>
  )
}

export default function CostSection({ projectId }: { projectId: string }) {
  const [window, setWindow] = useState('30d')
  const [groupBy, setGroupBy] = useState<'app' | 'environment'>('app')

  const { data, isLoading, error } = useQuery<CostReport>({
    queryKey: ['costs', projectId, window, groupBy],
    queryFn: () => api.getProjectCosts(projectId, window, groupBy),
  })

  const entries = data?.entries ?? []
  const currency = data?.total.currency ?? 'USD'
  const max = entries.reduce((m, e) => Math.max(m, e.cost.total), 0)

  // The sampler writes every five minutes, so a freshly-installed instance has nothing to
  // show and needs to be told that rather than being shown a convincing zero.
  const empty = !isLoading && !error && entries.length === 0

  return (
    <section className="card p-6">
      <div className="flex items-baseline justify-between mb-1">
        <h3 className="section-title">Cost</h3>
        <div className="flex items-center gap-2">
          <select
            value={groupBy}
            onChange={e => setGroupBy(e.target.value as 'app' | 'environment')}
            className="input-field text-xs py-1"
          >
            <option value="app">By app</option>
            <option value="environment">By environment</option>
          </select>
          <select
            value={window}
            onChange={e => setWindow(e.target.value)}
            className="input-field text-xs py-1"
          >
            {WINDOWS.map(w => (
              <option key={w.value} value={w.value}>{w.label}</option>
            ))}
          </select>
        </div>
      </div>

      <p className="text-xs text-text-tertiary mb-4">
        What these workloads reserve, priced against the rate card. Reserved rather than used,
        because a request occupies a node whether or not it is consumed.
      </p>

      {isLoading && <p className="text-xs text-text-tertiary">Loading&hellip;</p>}

      {error && (
        <p className="text-xs text-status-failed">
          Could not load costs: {(error as Error).message}
        </p>
      )}

      {empty && (
        <p className="text-xs text-text-tertiary">
          No samples yet. Usage is recorded every five minutes, so figures appear shortly after
          an app first runs.
        </p>
      )}

      {entries.length > 0 && (
        <>
          <div className="flex items-baseline gap-6 pb-4 mb-2 border-b border-border">
            <div>
              <div className="text-lg font-mono text-text-primary">{money(data?.total, currency)}</div>
              <div className="text-[10px] text-text-tertiary uppercase tracking-wide">
                measured over {WINDOWS.find(w => w.value === window)?.label ?? window}
              </div>
            </div>
            <div>
              <div className="text-lg font-mono text-text-secondary">
                {money(data?.projectedMonthly, currency)}
              </div>
              <div className="text-[10px] text-text-tertiary uppercase tracking-wide">
                projected monthly
              </div>
            </div>
          </div>

          <div className="flex items-center gap-3 pb-1 text-[10px] text-text-tertiary uppercase tracking-wide">
            <div className="w-40 shrink-0">{groupBy === 'app' ? 'App' : 'Environment'}</div>
            <div className="flex-1">cpu / memory / storage</div>
            <div className="w-24 text-right">Cost</div>
            <div className="w-20 text-right">Used</div>
          </div>

          <div className="divide-y divide-border/50">
            {entries.map(e => (
              <Bar key={e.key + (e.environment ?? '')} entry={e} max={max} currency={currency} />
            ))}
          </div>
        </>
      )}

      {data && (
        <p className="text-[10px] text-text-tertiary mt-4 pt-3 border-t border-border">
          {data.estimated ? (
            <>
              <span className="text-status-pending">Estimated.</span> Priced at the built-in default of{' '}
              {data.rates.currency} {data.rates.cpuCoreHour.toFixed(5)}/vCPU-hour and{' '}
              {data.rates.memoryGiBHour.toFixed(5)}/GiB-hour. Set your node's actual monthly cost
              in the platform config to price this cluster rather than a generic one.
            </>
          ) : (
            <>
              Priced at {data.rates.currency} {data.rates.cpuCoreHour.toFixed(5)}/vCPU-hour,{' '}
              {data.rates.memoryGiBHour.toFixed(5)}/GiB-hour and{' '}
              {data.rates.storageGiBMonth.toFixed(3)}/GiB-month.
            </>
          )}
        </p>
      )}
    </section>
  )
}
