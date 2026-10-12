// WebAuthn helpers: the server sends options with base64url-encoded
// buffers; the browser API needs ArrayBuffers.

function b64urlToBuf(s: string): ArrayBuffer {
  const pad = '='.repeat((4 - (s.length % 4)) % 4)
  const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out.buffer
}

function bufToB64url(b: ArrayBuffer | null): string | null {
  if (!b) return null
  const bytes = new Uint8Array(b)
  let bin = ''
  for (const x of bytes) bin += String.fromCharCode(x)
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export const webauthnSupported = () => typeof window !== 'undefined' && !!window.PublicKeyCredential

export async function createCredential(options: any): Promise<unknown> {
  const pk = options.publicKey
  pk.challenge = b64urlToBuf(pk.challenge)
  pk.user.id = b64urlToBuf(pk.user.id)
  if (pk.excludeCredentials) pk.excludeCredentials = pk.excludeCredentials.map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }))
  const cred = (await navigator.credentials.create({ publicKey: pk })) as PublicKeyCredential | null
  if (!cred) throw new Error('No credential created')
  const r = cred.response as AuthenticatorAttestationResponse
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      attestationObject: bufToB64url(r.attestationObject),
      transports: typeof r.getTransports === 'function' ? r.getTransports() : [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  }
}

export async function getAssertion(options: any): Promise<unknown> {
  const pk = options.publicKey
  pk.challenge = b64urlToBuf(pk.challenge)
  if (pk.allowCredentials) pk.allowCredentials = pk.allowCredentials.map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }))
  const cred = (await navigator.credentials.get({ publicKey: pk, mediation: options.mediation })) as PublicKeyCredential | null
  if (!cred) throw new Error('No credential returned')
  const r = cred.response as AuthenticatorAssertionResponse
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      authenticatorData: bufToB64url(r.authenticatorData),
      signature: bufToB64url(r.signature),
      userHandle: bufToB64url(r.userHandle),
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  }
}
