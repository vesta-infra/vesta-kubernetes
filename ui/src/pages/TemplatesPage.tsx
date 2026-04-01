import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'

const CATEGORIES = [
  { key: '', label: 'All' },
  { key: 'web', label: 'Web' },
  { key: 'runtime', label: 'Runtime' },
  { key: 'database', label: 'Database' },
  { key: 'messaging', label: 'Messaging' },
  { key: 'storage', label: 'Storage' },
]

export default function TemplatesPage() {
  const [category, setCategory] = useState('')
  const [search, setSearch] = useState('')
  const [deployingId, setDeployingId] = useState<string | null>(null)

  const { data: templates, isLoading } = useQuery({
    queryKey: ['templates', category, search],
    queryFn: () => api.listTemplates({ category: category || undefined, search: search || undefined }),
  })

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-display italic text-text-primary">Templates</h2>
        <div className="flex items-center gap-3">
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search templates..."
            className="input-field text-xs w-48"
          />
        </div>
      </div>

      <div className="flex gap-2">
        {CATEGORIES.map((cat) => (
          <button
            key={cat.key}
            onClick={() => setCategory(cat.key)}
            className={`px-3 py-1.5 rounded-lg text-xs font-mono transition-colors ${
              category === cat.key
                ? 'bg-accent/10 text-accent border border-accent/20'
                : 'bg-surface-1 text-text-tertiary border border-border hover:text-text-secondary'
            }`}
          >
            {cat.label}
          </button>
        ))}
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        </div>
      )}

      {templates?.items?.length === 0 && !isLoading && (
        <div className="card px-6 py-12 text-center">
          <p className="text-sm text-text-tertiary">No templates found</p>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {templates?.items?.map((tmpl: any) => (
          <div key={tmpl.id} className="card p-5 hover:border-accent/20 transition-colors group">
            <div className="flex items-start gap-3 mb-3">
              <span className="text-2xl">{tmpl.icon}</span>
              <div className="flex-1 min-w-0">
                <h3 className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors">
                  {tmpl.name}
                </h3>
                <span className="text-[11px] font-mono text-text-tertiary bg-surface-3 px-1.5 py-0.5 rounded mt-1 inline-block">
                  {tmpl.category}
                </span>
              </div>
            </div>
            <p className="text-xs text-text-secondary mb-3 line-clamp-2">{tmpl.description}</p>
            <div className="flex items-center justify-between">
              <span className="text-[11px] font-mono text-text-tertiary truncate">{tmpl.image}:{tmpl.tag}</span>
              <button
                onClick={() => setDeployingId(tmpl.id)}
                className="btn-primary text-[11px] px-3 py-1"
              >
                Deploy
              </button>
            </div>
          </div>
        ))}
      </div>

      {deployingId && (
        <DeployTemplateModal
          templateId={deployingId}
          templateName={templates?.items?.find((t: any) => t.id === deployingId)?.name || deployingId}
          onClose={() => setDeployingId(null)}
        />
      )}
    </div>
  )
}

function DeployTemplateModal({ templateId, templateName, onClose }: {
  templateId: string
  templateName: string
  onClose: () => void
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [name, setName] = useState(templateId)
  const [project, setProject] = useState('')
  const [selectedEnvs, setSelectedEnvs] = useState<string[]>([])

  const { data: projects } = useQuery({
    queryKey: ['projects'],
    queryFn: () => api.listProjects(),
  })

  const { data: environments } = useQuery({
    queryKey: ['environments', project],
    queryFn: () => api.listEnvironments(project),
    enabled: !!project,
  })

  const deployMutation = useMutation({
    mutationFn: () => api.deployTemplate(templateId, {
      project,
      name,
      environments: selectedEnvs,
    }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['apps'] })
      onClose()
      if (res?.id) navigate(`/apps/${res.id}`)
    },
  })

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50" onClick={onClose}>
      <div className="card p-6 w-full max-w-md animate-slide-up" onClick={(e) => e.stopPropagation()}>
        <h3 className="section-title mb-4">Deploy {templateName}</h3>
        <form onSubmit={(e) => { e.preventDefault(); deployMutation.mutate() }} className="space-y-4">
          <div>
            <label className="label">App Name</label>
            <input value={name} onChange={(e) => setName(e.target.value)} className="input-field" required />
          </div>
          <div>
            <label className="label">Project</label>
            <select value={project} onChange={(e) => { setProject(e.target.value); setSelectedEnvs([]) }} className="input-field" required>
              <option value="">Select project...</option>
              {projects?.items?.map((p: any) => (
                <option key={p.name || p.id} value={p.name || p.id}>
                  {p.displayName || p.name}
                </option>
              ))}
            </select>
          </div>
          {project && environments?.items && environments.items.length > 0 && (
            <div>
              <label className="label">Environments</label>
              <div className="flex flex-wrap gap-2">
                {environments.items.map((env: any) => (
                  <label key={env.name} className="flex items-center gap-1.5 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={selectedEnvs.includes(env.name)}
                      onChange={(e) => {
                        setSelectedEnvs(prev =>
                          e.target.checked ? [...prev, env.name] : prev.filter(n => n !== env.name)
                        )
                      }}
                      className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                    />
                    <span className="text-xs font-mono text-text-secondary">{env.name}</span>
                  </label>
                ))}
              </div>
            </div>
          )}
          <div className="flex gap-3 pt-2">
            <button type="submit" disabled={deployMutation.isPending || !project || !name} className="btn-primary">
              {deployMutation.isPending ? 'Deploying...' : 'Deploy'}
            </button>
            <button type="button" onClick={onClose} className="btn-ghost">Cancel</button>
          </div>
          {deployMutation.isError && (
            <p className="text-status-failed text-xs">{(deployMutation.error as Error).message}</p>
          )}
        </form>
      </div>
    </div>
  )
}
