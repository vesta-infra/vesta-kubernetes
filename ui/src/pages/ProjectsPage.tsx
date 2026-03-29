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

      <div className="space-y-2">
        {data?.items?.map((p: any, i: number) => (
          <Link
            key={p.id}
            to={`/projects/${p.id}`}
            className="card-hover flex items-center justify-between px-5 py-4 group block relative overflow-hidden"
            style={{ animationDelay: `${i * 0.03}s` }}
          >
            <div className="absolute left-0 top-0 bottom-0 w-px bg-gradient-to-b from-transparent via-accent/0 to-transparent group-hover:via-accent/30 transition-all duration-500" />
            <div className="flex items-center gap-4">
              <div className="w-10 h-10 rounded-lg bg-accent/[0.06] border border-accent/10 flex items-center justify-center group-hover:border-accent/25 group-hover:bg-accent/10 transition-all duration-300">
                <svg className="w-4 h-4 text-accent/70 group-hover:text-accent transition-colors duration-200" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                </svg>
              </div>
              <div>
                <p className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors duration-200">
                  {p.displayName || p.name}
                </p>
                <div className="flex items-center gap-2 mt-1">
                  <span className="text-[11px] text-text-tertiary font-mono">{p.name}</span>
                  {p.teamName && (
                    <span className="text-[10px] font-mono bg-surface-3/80 text-text-tertiary px-2 py-0.5 rounded border border-border/50">
                      {p.teamName}
                    </span>
                  )}
                </div>
              </div>
            </div>
            <div className="flex items-center gap-5">
              <div className="text-right">
                <span className="text-[11px] font-mono text-text-tertiary block">
                  {p.environmentCount ?? 0} env{(p.environmentCount ?? 0) !== 1 ? 's' : ''}
                </span>
                <span className="text-[11px] font-mono text-text-tertiary block mt-0.5">
                  {p.appCount ?? 0} app{(p.appCount ?? 0) !== 1 ? 's' : ''}
                </span>
              </div>
              {role !== 'viewer' && (
              <button
                onClick={(e) => {
                  e.preventDefault()
                  e.stopPropagation()
                  if (confirm(`Delete project "${p.name}"? This will remove all environments and apps.`))
                    deleteMutation.mutate(p.id)
                }}
                className="text-[11px] text-text-tertiary hover:text-status-failed transition-all duration-200 opacity-0 group-hover:opacity-100"
              >
                Delete
              </button>
              )}
            </div>
          </Link>
        ))}
      </div>
    </div>
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
