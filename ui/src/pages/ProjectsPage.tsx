import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'

export default function ProjectsPage() {
  const queryClient = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() })
  const [showCreate, setShowCreate] = useState(false)

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
        <button onClick={() => setShowCreate(!showCreate)} className="btn-primary">
          <span className="flex items-center gap-2">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            New Project
          </span>
        </button>
      </div>

      {showCreate && <CreateProjectForm onClose={() => setShowCreate(false)} />}

      {isLoading && <Spinner />}

      {!isLoading && data?.items?.length === 0 && (
        <div className="card px-6 py-12 text-center">
          <div className="w-10 h-10 rounded-xl bg-surface-3 flex items-center justify-center mx-auto mb-3">
            <svg className="w-5 h-5 text-text-tertiary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          </div>
          <p className="text-sm text-text-secondary">No projects yet</p>
          <p className="text-xs text-text-tertiary mt-1">Create your first project to start deploying.</p>
        </div>
      )}

      <div className="space-y-2">
        {data?.items?.map((p: any) => (
          <Link
            key={p.id}
            to={`/projects/${p.id}`}
            className="card-hover flex items-center justify-between px-5 py-4 group block"
          >
            <div className="flex items-center gap-4">
              <div className="w-9 h-9 rounded-lg bg-accent/10 flex items-center justify-center">
                <svg className="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
                </svg>
              </div>
              <div>
                <p className="text-sm font-medium text-text-primary group-hover:text-accent transition-colors">
                  {p.displayName || p.name}
                </p>
                <div className="flex items-center gap-2 mt-0.5">
                  <span className="text-xs text-text-tertiary font-mono">{p.name}</span>
                  {p.teamName && (
                    <span className="text-[11px] font-mono bg-surface-3 text-text-tertiary px-2 py-0.5 rounded">
                      {p.teamName}
                    </span>
                  )}
                </div>
              </div>
            </div>
            <div className="flex items-center gap-4">
              <span className="text-xs font-mono text-text-tertiary">
                {p.environmentCount ?? 0} env{(p.environmentCount ?? 0) !== 1 ? 's' : ''}
              </span>
              <span className="text-xs font-mono text-text-tertiary">
                {p.appCount ?? 0} app{(p.appCount ?? 0) !== 1 ? 's' : ''}
              </span>
              <button
                onClick={(e) => {
                  e.preventDefault()
                  e.stopPropagation()
                  if (confirm(`Delete project "${p.name}"? This will remove all environments and apps.`))
                    deleteMutation.mutate(p.id)
                }}
                className="text-xs text-text-tertiary hover:text-status-failed transition-colors opacity-0 group-hover:opacity-100"
              >
                Delete
              </button>
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
    <div className="flex items-center justify-center py-12">
      <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
      </svg>
    </div>
  )
}
