import { useState, useRef, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'

/**
 * Logs and a web terminal for whatever pods a source points at.
 *
 * Written for apps first and lifted out so add-ons get the same viewers. The pieces that
 * differ -- how logs are fetched, which pods exist, and the WebSocket base path the live
 * stream and the terminal connect to -- are the source; everything else is shared.
 */

export interface PodSummary {
  name: string
  containers?: { name: string }[]
}

export interface PodSource {
  /** Distinguishes cached queries between sources, e.g. `app:<id>`. */
  key: string
  /** Path the /logs/ws and /exec WebSockets hang off, e.g. `/api/v1/apps/<id>`. */
  wsBase: string
  getLogs: (environment: string, opts: { tail?: number; previous?: boolean }) => Promise<any>
  listPods: (environment: string) => Promise<PodSummary[]>
}

const POD_LOG_COLORS = ['#56d4dd', '#e5c07b', '#98c379', '#c678dd', '#d19a66', '#61afef']

function PodAge({ age, startedAt }: { age?: string; startedAt?: string }) {
  if (!age) return null
  return (
    <span
      className="flex items-center gap-1 text-[11px] text-text-tertiary"
      title={startedAt ? `Started ${new Date(startedAt).toLocaleString()}` : undefined}
    >
      <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 8v4l3 2m6-2a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span className="font-mono">{age}</span>
    </span>
  )
}

export function PodLogs({ source, environments, selectedEnv, onEnvChange, title = 'Logs' }: { source: PodSource; environments: string[]; selectedEnv: string; onEnvChange: (env: string) => void; title?: string }) {
  const env = selectedEnv || environments[0] || ''
  const setEnv = onEnvChange
  const [tail, setTail] = useState(100)
  const [previous, setPrevious] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [unified, setUnified] = useState(false)
  const [liveMode, setLiveMode] = useState(false)
  const [liveLines, setLiveLines] = useState<{ pod: string; line: string }[]>([])
  const [wsConnected, setWsConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const logEndRef = useRef<HTMLDivElement>(null)

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['podLogs', source.key, env, tail, previous],
    queryFn: () => source.getLogs(env, { tail, previous }),
    enabled: !!env && !liveMode,
    refetchInterval: autoRefresh ? 5000 : false,
  })

  // WebSocket streaming
  useEffect(() => {
    if (!liveMode || !env) {
      if (wsRef.current) {
        wsRef.current.close()
        wsRef.current = null
        setWsConnected(false)
      }
      return
    }

    const token = localStorage.getItem('vesta-token')
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${proto}//${window.location.host}${source.wsBase}/logs/ws?environment=${encodeURIComponent(env)}&tail=${tail}&token=${encodeURIComponent(token || '')}`
    const ws = new WebSocket(wsUrl)
    wsRef.current = ws
    setLiveLines([])

    ws.onopen = () => setWsConnected(true)
    ws.onclose = () => setWsConnected(false)
    ws.onerror = () => setWsConnected(false)
    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data)
        if (msg.type === 'log') {
          setLiveLines(prev => {
            const next = [...prev, { pod: msg.pod, line: msg.line }]
            return next.length > 2000 ? next.slice(-1500) : next
          })
        }
      } catch { /* ignore */ }
    }

    return () => {
      ws.close()
      wsRef.current = null
      setWsConnected(false)
    }
  }, [liveMode, env, source.wsBase, tail])

  useEffect(() => {
    if ((data || liveLines.length > 0) && logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [data, liveLines])

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
        <h3 className="section-title">{title}</h3>
        <div className="flex flex-wrap items-center gap-2">
          {env && (
            <>
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={autoRefresh}
                  onChange={(e) => setAutoRefresh(e.target.checked)}
                  className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                  disabled={liveMode}
                />
                <span className="text-[11px] text-text-tertiary">Auto-refresh</span>
              </label>
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={liveMode}
                  onChange={(e) => { setLiveMode(e.target.checked); if (e.target.checked) setAutoRefresh(false) }}
                  className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
                />
                <span className="text-[11px] text-text-tertiary flex items-center gap-1">
                  Live
                  {liveMode && wsConnected && <span className="inline-block w-1.5 h-1.5 rounded-full bg-status-running animate-glow-pulse" />}
                  {liveMode && !wsConnected && <span className="inline-block w-1.5 h-1.5 rounded-full bg-status-failed" />}
                </span>
              </label>
              <button
                onClick={() => refetch()}
                disabled={isFetching}
                className="btn-ghost text-xs"
              >
                {isFetching ? 'Loading...' : 'Refresh'}
              </button>
            </>
          )}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-3 mb-4">
        <div>
          <select
            value={env}
            onChange={(e) => setEnv(e.target.value)}
            className="input-field text-xs"
          >
            <option value="">Select environment...</option>
            {environments.map((e) => (
              <option key={e} value={e}>{e}</option>
            ))}
          </select>
        </div>
        <div>
          <select
            value={tail}
            onChange={(e) => setTail(Number(e.target.value))}
            className="input-field text-xs w-24"
          >
            <option value={50}>50 lines</option>
            <option value={100}>100 lines</option>
            <option value={500}>500 lines</option>
            <option value={1000}>1000 lines</option>
          </select>
        </div>
        <label className="flex items-center gap-1.5 cursor-pointer">
          <input
            type="checkbox"
            checked={previous}
            onChange={(e) => setPrevious(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="text-[11px] text-text-tertiary">Previous</span>
        </label>
        <label className="flex items-center gap-1.5 cursor-pointer">
          <input
            type="checkbox"
            checked={unified}
            onChange={(e) => setUnified(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-border bg-surface-1 text-accent focus:ring-accent/20"
          />
          <span className="text-[11px] text-text-tertiary">Unified</span>
        </label>
      </div>

      {!env && (
        <p className="text-sm text-text-tertiary">Select an environment to view logs</p>
      )}

      {env && isLoading && (
        <div className="flex items-center justify-center py-8">
          <svg className="w-5 h-5 animate-spin text-accent" fill="none" viewBox="0 0 24 24">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
        </div>
      )}

      {env && data && (
        <div className="space-y-3">
          {data.total === 0 && (
            <p className="text-sm text-text-tertiary">No pods found in this environment</p>
          )}
          {unified && data.pods?.length > 0 ? (
            <div className="border border-border rounded-lg overflow-hidden">
              <div className="bg-surface-1 px-4 py-2.5 flex items-center gap-4 border-b border-border flex-wrap">
                {data.pods.map((pod: any, i: number) => (
                  <div key={pod.pod} className="flex items-center gap-1.5">
                    <span className="inline-block w-2 h-2 rounded-full" style={{ backgroundColor: POD_LOG_COLORS[i % POD_LOG_COLORS.length] }} />
                    <span className="text-[11px] font-mono text-text-secondary">{pod.pod.split('-').slice(-2).join('-')}</span>
                    <span className="text-[10px] text-text-tertiary" title={pod.startedAt ? `Started ${new Date(pod.startedAt).toLocaleString()}` : undefined}>
                      ({pod.status}{pod.age ? ` · ${pod.age}` : ''})
                    </span>
                  </div>
                ))}
              </div>
              <pre className="bg-[#0d1117] text-[#c9d1d9] text-[11px] leading-relaxed p-4 overflow-x-auto max-h-[600px] overflow-y-auto font-mono whitespace-pre-wrap break-all">
                {data.pods.flatMap((pod: any, i: number) => {
                  const color = POD_LOG_COLORS[i % POD_LOG_COLORS.length]
                  const short = pod.pod.split('-').slice(-2).join('-')
                  const lines = (pod.logs || '').split('\n')
                  return lines.map((line: string, li: number) => (
                    <span key={`${pod.pod}-${li}`}>
                      <span style={{ color }}>[{short}]</span> {line}{'\n'}
                    </span>
                  ))
                })}
                <div ref={logEndRef} />
              </pre>
            </div>
          ) : (
            data.pods?.map((pod: any) => (
            <div key={pod.pod} className="border border-border rounded-lg overflow-hidden">
              <div className="bg-surface-1 px-4 py-2.5 flex items-center justify-between border-b border-border">
                <div className="flex items-center gap-3">
                  <span className={`inline-block w-2 h-2 rounded-full ${
                    pod.status === 'Running' ? 'bg-status-running' :
                    pod.status === 'Failed' || pod.status === 'Error' ? 'bg-status-failed' :
                    'bg-status-pending'
                  }`} />
                  <span className="text-xs font-mono text-text-primary">{pod.pod}</span>
                </div>
                <div className="flex items-center gap-3">
                  <span className="text-[11px] text-text-tertiary">{pod.status}</span>
                  <PodAge age={pod.age} startedAt={pod.startedAt} />
                  {pod.restarts > 0 && (
                    <span className="text-[11px] text-status-failed">{pod.restarts} restart{pod.restarts !== 1 ? 's' : ''}</span>
                  )}
                </div>
              </div>
              <pre className="bg-[#0d1117] text-[#c9d1d9] text-[11px] leading-relaxed p-4 overflow-x-auto max-h-80 overflow-y-auto font-mono whitespace-pre-wrap break-all">
                {pod.logs || '(no output)'}
                <div ref={logEndRef} />
              </pre>
            </div>
          ))
          )}
        </div>
      )}

      {env && liveMode && (
        <div className="border border-border rounded-lg overflow-hidden">
          <div className="bg-surface-1 px-4 py-2.5 flex items-center justify-between border-b border-border">
            <div className="flex items-center gap-2">
              <span className={`inline-block w-2 h-2 rounded-full ${wsConnected ? 'bg-status-running animate-glow-pulse' : 'bg-status-failed'}`} />
              <span className="text-xs font-mono text-text-primary">Live Stream</span>
            </div>
            <span className="text-[11px] text-text-tertiary">{liveLines.length} lines</span>
          </div>
          <pre className="bg-[#0d1117] text-[#c9d1d9] text-[11px] leading-relaxed p-4 overflow-x-auto max-h-[600px] overflow-y-auto font-mono whitespace-pre-wrap break-all">
            {liveLines.length === 0 && <span className="text-text-tertiary">Waiting for logs...</span>}
            {liveLines.map((entry, i) => {
              const pods = [...new Set(liveLines.map(l => l.pod))]
              const podIdx = pods.indexOf(entry.pod)
              const color = POD_LOG_COLORS[podIdx % POD_LOG_COLORS.length]
              const short = entry.pod.split('-').slice(-2).join('-')
              return (
                <span key={i}>
                  <span style={{ color }}>[{short}]</span> {entry.line}{'\n'}
                </span>
              )
            })}
            <div ref={logEndRef} />
          </pre>
        </div>
      )}
    </div>
  )
}

export function PodTerminal({ source, environments, selectedEnv, onEnvChange, title = 'Web Terminal' }: { source: PodSource; environments: string[]; selectedEnv: string; onEnvChange: (env: string) => void; title?: string }) {
  const env = selectedEnv || environments[0] || ''
  const setEnv = onEnvChange
  const [pod, setPod] = useState('')
  const [container, setContainer] = useState('')
  const [connected, setConnected] = useState(false)
  const termRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const xtermRef = useRef<any>(null)

  const { data: podList } = useQuery({
    queryKey: ['podList', source.key, env],
    queryFn: () => source.listPods(env),
    enabled: !!env,
  })

  const pods: PodSummary[] = podList || []
  const podNames = pods.map(p => p.name)
  const selectedPod = pods.find(p => p.name === pod)
  const containers: string[] = (selectedPod?.containers || []).map(c => c.name)

  useEffect(() => {
    if (podNames.length > 0 && !pod) setPod(podNames[0])
  }, [podNames.length])

  useEffect(() => {
    if (containers.length > 0 && !containers.includes(container)) setContainer(containers[0])
  }, [pod, containers.length])

  const connect = () => {
    if (!env || !pod || !termRef.current) return

    // Dynamically import xterm
    Promise.all([
      import('@xterm/xterm'),
      import('@xterm/addon-fit'),
    ]).then(([{ Terminal }, { FitAddon }]) => {
      // Clean up previous
      if (xtermRef.current) {
        xtermRef.current.dispose()
      }
      if (wsRef.current) {
        wsRef.current.close()
      }

      const term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
        theme: {
          background: '#0d1117',
          foreground: '#c9d1d9',
          cursor: '#58a6ff',
          selectionBackground: '#264f78',
        },
      })
      const fitAddon = new FitAddon()
      term.loadAddon(fitAddon)
      term.open(termRef.current!)
      fitAddon.fit()
      xtermRef.current = term

      const token = localStorage.getItem('vesta-token')
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const params = new URLSearchParams({ environment: env, pod, token: token || '' })
      if (container) params.set('container', container)
      const wsUrl = `${proto}//${window.location.host}${source.wsBase}/exec?${params}`
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws

      ws.onopen = () => {
        setConnected(true)
        term.focus()
      }
      ws.onclose = () => {
        setConnected(false)
        term.write('\r\n\x1b[31m--- Connection closed ---\x1b[0m\r\n')
      }
      ws.onerror = () => {
        setConnected(false)
      }
      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          if (msg.type === 'output') {
            term.write(msg.data)
          } else if (msg.type === 'error') {
            term.write(`\r\n\x1b[31m${msg.message}\x1b[0m\r\n`)
          }
        } catch {
          term.write(event.data)
        }
      }

      term.onData((data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'input', data }))
        }
      })

      // Handle resize
      const resizeObserver = new ResizeObserver(() => fitAddon.fit())
      resizeObserver.observe(termRef.current!)

      return () => {
        resizeObserver.disconnect()
      }
    })
  }

  useEffect(() => {
    return () => {
      if (wsRef.current) wsRef.current.close()
      if (xtermRef.current) xtermRef.current.dispose()
    }
  }, [])

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
        <h3 className="section-title">{title}</h3>
        <div className="flex items-center gap-2">
          {connected && <span className="inline-block w-2 h-2 rounded-full bg-status-running animate-glow-pulse" />}
          <span className="text-[11px] text-text-tertiary">{connected ? 'Connected' : 'Disconnected'}</span>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-3 mb-4">
        <select value={env} onChange={(e) => { setEnv(e.target.value); setPod(''); setContainer('') }} className="input-field text-xs">
          <option value="">Select environment...</option>
          {environments.map((e) => (
            <option key={e} value={e}>{e}</option>
          ))}
        </select>
        <select value={pod} onChange={(e) => { setPod(e.target.value); setContainer('') }} className="input-field text-xs w-auto font-mono">
          <option value="">Select pod...</option>
          {podNames.map((p: string) => (
            <option key={p} value={p}>{p}</option>
          ))}
        </select>
        {containers.length > 1 && (
          <select value={container} onChange={(e) => setContainer(e.target.value)} className="input-field text-xs w-auto font-mono">
            {containers.map((c) => (
              <option key={c} value={c}>{c}</option>
            ))}
          </select>
        )}
        <button
          onClick={connect}
          disabled={!env || !pod}
          className="btn-primary text-xs"
        >
          {connected ? 'Reconnect' : 'Connect'}
        </button>
        {connected && (
          <button
            onClick={() => {
              if (wsRef.current) wsRef.current.close()
              setConnected(false)
            }}
            className="btn-ghost text-xs text-status-failed"
          >
            Disconnect
          </button>
        )}
      </div>

      <div
        ref={termRef}
        className="border border-border rounded-lg overflow-hidden bg-[#0d1117]"
        style={{ minHeight: '400px' }}
      />
    </div>
  )
}
