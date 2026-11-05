import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type CostSettingsInput } from '../lib/api'

/**
 * The rate card cost pages price against.
 *
 * A node's price is asked for first because it is the question an administrator can
 * answer: what a machine costs is on an invoice, what a vCPU-hour is worth is not. Per-unit
 * rates stay available for clusters billed that way, under Advanced.
 */

const HOURS_PER_MONTH = 730

/** Mirrors RatesFromNode in api/internal/services/cost.go: half the node to CPU, half to memory. */
function deriveRates(monthly: number, vcpus: number, memGiB: number) {
  const half = monthly / HOURS_PER_MONTH / 2
  return { cpu: half / vcpus, mem: half / memGiB }
}

const num = (s: string) => (s.trim() === '' ? undefined : Number(s))
const str = (v: unknown) => (v === undefined || v === null ? '' : String(v))

export default function CostSettings() {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['costSettings'], queryFn: api.getCostSettings })

  const [nodeCost, setNodeCost] = useState('')
  const [nodeVCPUs, setNodeVCPUs] = useState('')
  const [nodeMem, setNodeMem] = useState('')
  const [currency, setCurrency] = useState('USD')
  const [cpuRate, setCpuRate] = useState('')
  const [memRate, setMemRate] = useState('')
  const [storageRate, setStorageRate] = useState('')
  const [advanced, setAdvanced] = useState(false)

  useEffect(() => {
    if (!data) return
    const c = data.config || {}
    setNodeCost(str(c.nodeMonthlyCost))
    setNodeVCPUs(str(c.nodeVCPUs))
    setNodeMem(str(c.nodeMemoryGiB))
    setCurrency(str(c.currency) || 'USD')
    setCpuRate(str(c.cpuCoreHour))
    setMemRate(str(c.memoryGiBHour))
    setStorageRate(str(c.storageGiBMonth))
    setAdvanced(!!(c.cpuCoreHour || c.memoryGiBHour || c.storageGiBMonth))
  }, [data])

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['costSettings'] })
    // Every cost figure on screen was priced with the old card.
    queryClient.invalidateQueries({ queryKey: ['costs'] })
    queryClient.invalidateQueries({ queryKey: ['appCosts'] })
  }
  const save = useMutation({ mutationFn: (body: CostSettingsInput) => api.setCostSettings(body), onSuccess: invalidate })

  if (isLoading || !data) return null

  const nodeValues = [num(nodeCost), num(nodeVCPUs), num(nodeMem)]
  const nodeGiven = nodeValues.filter(v => v !== undefined).length
  const nodeComplete = nodeGiven === 3 && nodeValues.every(v => (v as number) > 0)
  const nodeError = nodeGiven > 0 && !nodeComplete ? 'Give all three, each above zero — or clear them all.' : ''
  const preview = nodeComplete ? deriveRates(nodeValues[0]!, nodeValues[1]!, nodeValues[2]!) : null

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (nodeError) return
    save.mutate({
      ...(nodeComplete && { nodeMonthlyCost: nodeValues[0], nodeVCPUs: nodeValues[1], nodeMemoryGiB: nodeValues[2] }),
      ...(advanced && num(cpuRate) !== undefined && { cpuCoreHour: num(cpuRate) }),
      ...(advanced && num(memRate) !== undefined && { memoryGiBHour: num(memRate) }),
      ...(advanced && num(storageRate) !== undefined && { storageGiBMonth: num(storageRate) }),
      currency: currency.trim() || 'USD',
    })
  }

  const eff = data.effective

  return (
    <section className="card p-6">
      <h3 className="section-title mb-1">Cost</h3>
      <p className="text-xs text-text-tertiary mb-5">
        What the project cost pages price against. Workloads are charged for what they reserve,
        or what they use when they burst above it. Enter what one of your nodes costs and Vesta
        splits it evenly between CPU and memory.
      </p>

      <form onSubmit={submit} className="space-y-5">
        <div className="grid grid-cols-1 sm:grid-cols-4 gap-4">
          <div>
            <label className="label">Node monthly cost</label>
            <input type="number" step="any" min="0" value={nodeCost} onChange={e => setNodeCost(e.target.value)} className="input-field" placeholder="200" />
          </div>
          <div>
            <label className="label">vCPUs</label>
            <input type="number" step="any" min="0" value={nodeVCPUs} onChange={e => setNodeVCPUs(e.target.value)} className="input-field" placeholder="8" />
          </div>
          <div>
            <label className="label">Memory (GiB)</label>
            <input type="number" step="any" min="0" value={nodeMem} onChange={e => setNodeMem(e.target.value)} className="input-field" placeholder="32" />
          </div>
          <div>
            <label className="label">Currency</label>
            <input value={currency} onChange={e => setCurrency(e.target.value.toUpperCase())} className="input-field font-mono" maxLength={3} placeholder="USD" />
          </div>
        </div>
        {nodeError && <p className="text-[11px] text-status-failed">{nodeError}</p>}
        {preview && (
          <p className="text-[11px] text-text-secondary">
            Works out to <span className="font-mono text-text-primary">{currency} {preview.cpu.toFixed(5)}</span>/vCPU-hour and{' '}
            <span className="font-mono text-text-primary">{currency} {preview.mem.toFixed(5)}</span>/GiB-hour.
          </p>
        )}

        <details open={advanced} onToggle={e => setAdvanced((e.target as HTMLDetailsElement).open)} className="rounded-lg border border-border bg-surface-1 p-3">
          <summary className="text-xs text-text-secondary cursor-pointer">Advanced: set per-unit rates instead</summary>
          <p className="text-[11px] text-text-tertiary mt-2">
            Used when no node cost is given. When both are set, the node cost wins for CPU and memory; storage is always priced from here.
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mt-3">
            <div>
              <label className="label">CPU / vCPU-hour</label>
              <input type="number" step="any" min="0" value={cpuRate} onChange={e => setCpuRate(e.target.value)} className="input-field font-mono" placeholder={String(data.default.cpuCoreHour)} />
            </div>
            <div>
              <label className="label">Memory / GiB-hour</label>
              <input type="number" step="any" min="0" value={memRate} onChange={e => setMemRate(e.target.value)} className="input-field font-mono" placeholder={String(data.default.memoryGiBHour)} />
            </div>
            <div>
              <label className="label">Storage / GiB-month</label>
              <input type="number" step="any" min="0" value={storageRate} onChange={e => setStorageRate(e.target.value)} className="input-field font-mono" placeholder={String(data.default.storageGiBMonth)} />
            </div>
          </div>
        </details>

        <div className="flex flex-wrap items-center gap-3">
          <button type="submit" disabled={save.isPending || !!nodeError} className="btn-primary">
            {save.isPending ? 'Saving…' : 'Save rates'}
          </button>
          <button
            type="button"
            disabled={save.isPending || data.estimated}
            onClick={() => save.mutate({})}
            className="btn-ghost text-xs"
          >
            Reset to default
          </button>
          {save.error && <span className="text-xs text-status-failed">{(save.error as Error).message}</span>}
        </div>
      </form>

      <p className="text-[11px] text-text-tertiary mt-5 pt-4 border-t border-border">
        {data.estimated ? <><span className="text-status-pending">Using the built-in estimate:</span>{' '}</> : 'In effect: '}
        <span className="font-mono">{eff.currency} {eff.cpuCoreHour.toFixed(5)}</span>/vCPU-hour,{' '}
        <span className="font-mono">{eff.memoryGiBHour.toFixed(5)}</span>/GiB-hour,{' '}
        <span className="font-mono">{eff.storageGiBMonth.toFixed(3)}</span>/GiB-month.
      </p>
    </section>
  )
}
