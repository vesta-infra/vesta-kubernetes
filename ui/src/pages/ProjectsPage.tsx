import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useUserRole } from '../lib/useRole'

export default function ProjectsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })
  const [showCreate, setShowCreate] = useState(false)
  const role = useUserRole()

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteProject(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['projects'] }),
  })

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <p className="text-sm text-text-secondary">
          {data?.total ?? 0} project{(data?.total ?? 0) !== 1 ? 's' : ''}
        </p>
        {role !== 'viewer' && (
        <button onClick={() => setShowCreate(!showCreate)} className="btn-primary">
          <span className="flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            New Project
          </span>
        </button>
        )}
      </div>

      {showCreate && <CreateProjectForm onClose={() => setShowCreate(false)} />}

      {isLoading && <Spinner />}

      {!isLoading && data?.items?.length === 0 && (
        <div className="card px-6 py-16 text-center gradient-border">
          <div className="w-12 h-12 rounded-xl bg-surface-3 border border-border flex items-center justify-center mx-auto mb-4">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary font-medium">No projects yet</p>
          <p className="text-xs text-text-tertiary mt-1.5">Create your first project to start deploying.</p>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
        {data?.items?.map((p: any, i: number) => (
          <ProjectCard
            key={p.id}
            project={p}
            index={i}
            canDelete={role !== 'viewer'}
            onDelete={() => {
              if (confirm(`Delete project "${p.name}"? This will remove all environments and apps.`))
                deleteMutation.mutate(p.id)
            }}
          />
        ))}
      </div>
    </div>
  )
}

function formatAge(dateStr?: string): string {
  if (!dateStr) return ''
  const t = new Date(dateStr).getTime()
  if (Number.isNaN(t)) return ''
  const days = Math.floor((Date.now() - t) / 86400000)
  if (days < 1) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 30) return `${days}d ago`
  const months = Math.floor(days / 30)
  if (months < 12) return `${months}mo ago`
  return `${Math.floor(days / 365)}y ago`
}

function ProjectCard({ project: p, index, canDelete, onDelete }: { project: any; index: number; canDelete: boolean; onDelete: () => void }) {
  const age = formatAge(p.createdAt)

  return (
    <Link
      to={`/projects/${p.id}`}
      className="card-hover top-sheen group relative flex flex-col p-5 h-full animate-slide-up"
      style={{ animationDelay: `${index * 0.04}s` }}
    >
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-lg bg-accent/[0.06] border border-accent/10 flex items-center justify-center shrink-0 group-hover:border-accent/25 group-hover:bg-accent/10 transition-all duration-300">
          <svg className="w-4 h-4 text-accent/70 group-hover:text-accent transition-colors duration-200" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
          </svg>
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-text-primary truncate group-hover:text-accent transition-colors duration-200">
            {p.displayName || p.name}
          </p>
          {(p.displayName && p.displayName !== p.name) && (
            <p className="text-[11px] font-mono text-text-tertiary truncate mt-0.5">{p.name}</p>
          )}
        </div>
        {canDelete && (
          <button
            onClick={(e) => { e.preventDefault(); e.stopPropagation(); onDelete() }}
            title={`Delete ${p.name}`}
            aria-label={`Delete ${p.name}`}
            className="shrink-0 p-1.5 -m-1 rounded-md text-text-quaternary opacity-0 group-hover:opacity-100
                       hover:text-status-failed hover:bg-status-failed/10 transition-all duration-200"
          >
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M4 7h16M9 7V4h6v3M6 7l1 14h10l1-14" />
            </svg>
          </button>
        )}
      </div>

      {(p.teamName || p.spec?.team) && (
        <div className="mt-3">
          <span className="chip">{p.teamName || p.spec.team}</span>
        </div>
      )}

      {/* Counts carry the card's weight, so they get figure treatment. */}
      <div className="mt-auto grid grid-cols-2 gap-3 pt-5 mt-5 border-t border-border/60">
        <div>
          <p className="kpi-label mb-1.5">Environments</p>
          <p className="text-lg font-semibold tabular text-text-primary">{p.environmentCount ?? 0}</p>
        </div>
        <div>
          <p className="kpi-label mb-1.5">Apps</p>
          <p className="text-lg font-semibold tabular text-text-primary">{p.appCount ?? 0}</p>
        </div>
      </div>

      <div className="flex items-center justify-between mt-4 pt-3 border-t border-border/40">
        <span className="text-[10px] font-mono text-text-quaternary" title={p.createdAt ? new Date(p.createdAt).toLocaleString() : undefined}>
          {age ? `created ${age}` : ''}
        </span>
        <svg className="w-3.5 h-3.5 text-text-quaternary group-hover:text-accent group-hover:translate-x-0.5 transition-all duration-300" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
        </svg>
      </div>
    </Link>
  )
}

function CreateProjectForm({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [teamId, setTeamId] = useState('')

  const { data: teams } = useQuery({ queryKey: ['teams'], queryFn: () => api.listTeams() })

  const mutation = useMutation({
    mutationFn: (data: any) => api.createProject(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
      onClose()
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    mutation.mutate({
      name,
      displayName: displayName || undefined,
      team: teamId,
    })
  }

  return (
    <form onSubmit={handleSubmit} className="card p-5 space-y-4 animate-slide-up">
      <h3 className="section-title">Create Project</h3>
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="label">Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="input-field"
            required
            placeholder="my-project"
          />
        </div>
        <div>
          <label className="label">Display Name</label>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            className="input-field"
            placeholder="My Project"
          />
        </div>
      </div>
      <div>
        <label className="label">Team</label>
        <select value={teamId} onChange={(e) => setTeamId(e.target.value)} className="input-field" required>
          <option value="">Select a team</option>
          {teams?.items?.map((t: any) => (
            <option key={t.id} value={t.id}>{t.displayName || t.name}</option>
          ))}
        </select>
      </div>
      <div className="flex gap-3 pt-1">
        <button type="submit" disabled={mutation.isPending} className="btn-primary">
          {mutation.isPending ? 'Creating...' : 'Create Project'}
        </button>
        <button type="button" onClick={onClose} className="btn-ghost">
          Cancel
        </button>
      </div>
      {mutation.isError && (
        <p className="text-status-failed text-xs">{(mutation.error as Error).message}</p>
      )}
    </form>
  )
}

function Spinner() {
  return (
    <div className="flex items-center justify-center py-16">
      <div className="relative">
        <div className="w-8 h-8 rounded-lg bg-accent/10 border border-accent/20 flex items-center justify-center animate-glow-pulse">
          <div className="w-2.5 h-2.5 rounded bg-accent" />
        </div>
      </div>
    </div>
  )
}
