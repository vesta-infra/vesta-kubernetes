import { useState } from 'react'
import type { InputHTMLAttributes } from 'react'

type RevealableInputProps = InputHTMLAttributes<HTMLInputElement>

export default function RevealableInput({
  type,
  className = '',
  ...props
}: RevealableInputProps) {
  const [revealed, setRevealed] = useState(false)
  const isSecret = type === 'password'

  if (!isSecret) {
    return <input type={type} className={className} {...props} />
  }

  return (
    <div className="relative">
      <input
        {...props}
        type={revealed ? 'text' : 'password'}
        className={`${className} pr-16`}
      />
      <button
        type="button"
        onClick={() => setRevealed((v) => !v)}
        className="absolute right-3 top-1/2 -translate-y-1/2 text-[11px] font-mono text-text-tertiary hover:text-accent transition-colors"
        aria-label={revealed ? 'Hide secret value' : 'Reveal secret value'}
      >
        {revealed ? 'Hide' : 'Show'}
      </button>
    </div>
  )
}
