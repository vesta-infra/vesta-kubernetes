// Kubernetes rejects Secret keys longer than 253 characters or containing anything
// outside [-._a-zA-Z0-9]. That rejection happens in the operator, far from where the key
// was typed, and leaves a VestaSecret that can never sync. Validate at entry instead.
export const MAX_SECRET_KEY_LENGTH = 253

const SECRET_KEY_PATTERN = /^[-._a-zA-Z0-9]+$/

/** Returns an error message for an invalid key, or null when the key is usable. */
export function secretKeyError(key: string): string | null {
  if (!key) return 'Key must not be empty'
  if (key.length > MAX_SECRET_KEY_LENGTH) {
    // A key this long is nearly always a value pasted into the key field, so say so
    // rather than just quoting the limit.
    return `Key is ${key.length} characters (max ${MAX_SECRET_KEY_LENGTH}) — did you paste a value into the key field?`
  }
  if (key === '.' || key === '..') return `Key must not be "${key}"`
  if (!SECRET_KEY_PATTERN.test(key)) {
    return "Key may contain only letters, digits, '-', '_' and '.'"
  }
  return null
}

export function isValidSecretKey(key: string): boolean {
  return secretKeyError(key) === null
}

/**
 * Parses .env-style text into key/value pairs, separating out lines whose key Kubernetes
 * would reject. A PEM or other multi-line value pasted unquoted lands here: its base64
 * continuation lines end in "=" padding, so they parse as a key with an empty value.
 */
export function parseEnvContent(content: string): {
  data: Record<string, string>
  invalidKeys: string[]
} {
  const data: Record<string, string> = {}
  const invalidKeys: string[] = []

  for (const line of content.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const eqIdx = trimmed.indexOf('=')
    if (eqIdx === -1) continue
    const key = trimmed.slice(0, eqIdx).trim()
    let value = trimmed.slice(eqIdx + 1).trim()
    if (value.startsWith('"') && value.endsWith('"') && value.length >= 2) {
      // Double quotes are the escaping form formatEnvContent emits, so undo the escapes
      // here. Without this the two functions disagree and copied output re-imports wrong.
      value = unescapeEnvValue(value.slice(1, -1))
    } else if (value.startsWith("'") && value.endsWith("'") && value.length >= 2) {
      value = value.slice(1, -1)
    }
    if (!key) continue
    if (!isValidSecretKey(key)) {
      invalidKeys.push(key)
      continue
    }
    data[key] = value
  }

  return { data, invalidKeys }
}

/**
 * A value needing quotes to survive a bare KEY=value line: parseEnvContent trims each
 * side, treats a leading # as a comment, and splits on newlines, so any of those
 * characters would come back different from what went out.
 */
const NEEDS_QUOTING = /^\s|\s$|[\n\r\t"'#\\]/

function escapeEnvValue(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')
    .replace(/\t/g, '\\t')
}

function unescapeEnvValue(value: string): string {
  return value.replace(/\\([nrt"'\\])/g, (_, ch) => {
    switch (ch) {
      case 'n': return '\n'
      case 'r': return '\r'
      case 't': return '\t'
      default: return ch
    }
  })
}

/**
 * Serialises key/value pairs to .env text — the inverse of parseEnvContent, so copied
 * output pastes straight back into an import box. Keys are sorted to make two copies of
 * the same config diffable; Kubernetes does not preserve map order anyway.
 */
export function formatEnvContent(data: Record<string, string>): string {
  return Object.keys(data)
    .sort()
    .map((key) => {
      const value = data[key] ?? ''
      return `${key}=${NEEDS_QUOTING.test(value) ? `"${escapeEnvValue(value)}"` : value}`
    })
    .join('\n')
}

/** Truncates a key for display. An over-long key is usually secret material. */
export function truncateSecretKey(key: string, shown = 24): string {
  return key.length <= shown ? key : `${key.slice(0, shown)}… (${key.length} chars)`
}
