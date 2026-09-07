/**
 * Browser-side WebAuthn plumbing.
 *
 * The credentials API speaks ArrayBuffer while JSON speaks strings, so every ceremony
 * needs the same base64url conversion in both directions. Base64URL specifically -- the
 * standard alphabet's + and / are not URL-safe and the server encodes with the raw URL
 * alphabet, so decoding with atob alone would corrupt roughly one credential id in eight.
 */

export function isWebAuthnAvailable(): boolean {
  return typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential !== 'undefined' &&
    // Undefined outside a secure context, which is the normal case for a self-hosted
    // Vesta reached over plain http.
    typeof navigator.credentials?.create === 'function'
}

function base64UrlToBuffer(value: string): ArrayBuffer {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/')
  const binary = atob(padded.padEnd(padded.length + ((4 - (padded.length % 4)) % 4), '='))
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

function bufferToBase64Url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i])
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** Decodes the server's creation options into what navigator.credentials.create expects. */
function decodeCreationOptions(publicKey: any): PublicKeyCredentialCreationOptions {
  return {
    ...publicKey,
    challenge: base64UrlToBuffer(publicKey.challenge),
    user: { ...publicKey.user, id: base64UrlToBuffer(publicKey.user.id) },
    excludeCredentials: (publicKey.excludeCredentials || []).map((c: any) => ({
      ...c,
      id: base64UrlToBuffer(c.id),
    })),
  }
}

function decodeRequestOptions(publicKey: any): PublicKeyCredentialRequestOptions {
  return {
    ...publicKey,
    challenge: base64UrlToBuffer(publicKey.challenge),
    allowCredentials: (publicKey.allowCredentials || []).map((c: any) => ({
      ...c,
      id: base64UrlToBuffer(c.id),
    })),
  }
}

/** Runs a registration ceremony and returns the JSON the server's parser expects. */
export async function createCredential(publicKey: any): Promise<any> {
  const credential = (await navigator.credentials.create({
    publicKey: decodeCreationOptions(publicKey),
  })) as PublicKeyCredential | null

  if (!credential) throw new Error('No passkey was created')
  const response = credential.response as AuthenticatorAttestationResponse

  return {
    id: credential.id,
    rawId: bufferToBase64Url(credential.rawId),
    type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: bufferToBase64Url(response.clientDataJSON),
      attestationObject: bufferToBase64Url(response.attestationObject),
      // Transports let the browser hint how to reach this authenticator next time. Not
      // every authenticator reports them, hence the guard.
      transports: typeof response.getTransports === 'function' ? response.getTransports() : [],
    },
  }
}

/** Runs an authentication ceremony and returns the JSON the server's parser expects. */
export async function getAssertion(publicKey: any): Promise<any> {
  const credential = (await navigator.credentials.get({
    publicKey: decodeRequestOptions(publicKey),
  })) as PublicKeyCredential | null

  if (!credential) throw new Error('No passkey was used')
  const response = credential.response as AuthenticatorAssertionResponse

  return {
    id: credential.id,
    rawId: bufferToBase64Url(credential.rawId),
    type: credential.type,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: bufferToBase64Url(response.clientDataJSON),
      authenticatorData: bufferToBase64Url(response.authenticatorData),
      signature: bufferToBase64Url(response.signature),
      userHandle: response.userHandle ? bufferToBase64Url(response.userHandle) : null,
    },
  }
}

/**
 * Turns a ceremony failure into something worth showing.
 *
 * The DOMExceptions browsers raise here are famously unhelpful -- NotAllowedError covers
 * both "the user cancelled" and "the request timed out" -- so the common cases get their
 * own wording rather than surfacing a bare error name.
 */
export function describeWebAuthnError(err: unknown): string {
  const e = err as DOMException
  switch (e?.name) {
    case 'NotAllowedError':
      return 'Passkey prompt was dismissed or timed out. Try again.'
    case 'InvalidStateError':
      return 'This device already has a passkey registered for your account.'
    case 'NotSupportedError':
      return 'This device does not support the required passkey type.'
    case 'SecurityError':
      return 'Passkeys require the site to be served over HTTPS.'
    case 'AbortError':
      return 'Passkey request was cancelled.'
    default:
      return (err as Error)?.message || 'Passkey request failed'
  }
}
