import type { InstallInfo, InstallPackage } from './types'

export type WritePolicy = 'server' | 'operator' | 'none'
export type Platform = 'deb' | 'rpm' | 'script' | 'windows'
export type Arch = 'amd64' | 'arm64'

export interface CmdOptions {
  info: Pick<InstallInfo, 'server_url' | 'tls_mode' | 'ca_fingerprint' | 'spki_sha256'>
  token: string
  policy: WritePolicy
  operatorKeys: string[]
  docker: boolean
}

export const TOKEN_PLACEHOLDER = 'XXXX-XXXX-XXXX'
export const KEY_PLACEHOLDER = '<OPERATOR_PUBLIC_KEY>'

// Operator keys are pasted by the user and end up inside a shell command, so
// only characters of a base64 key are accepted.
const keyRe = /^[A-Za-z0-9+/=_-]+$/
export function parseOperatorKeys(s: string): { keys: string[]; invalid: boolean } {
  const parts = s.split(/[\s,]+/).filter(Boolean)
  return { keys: parts.filter((k) => keyRe.test(k)), invalid: parts.some((k) => !keyRe.test(k)) }
}

/** Pinning applies to a self-signed certificate whose public key hash is known. */
export const pinOf = (i: CmdOptions['info']) => (i.tls_mode === 'selfsigned' && i.spki_sha256 ? i.spki_sha256 : '')
const caOf = (i: CmdOptions['info']) => (i.tls_mode === 'selfsigned' ? i.ca_fingerprint : '')

// PowerShell validation callback: accepts only a server certificate whose
// SubjectPublicKeyInfo hashes to $p. The self-signed certificate is ECDSA
// P-256, so the SPKI is the fixed algorithm header plus the raw key point.
export const PS_PIN_CALLBACK =
  '[Net.ServicePointManager]::ServerCertificateValidationCallback={param($s,$c,$ch,$e) [Convert]::ToBase64String([Security.Cryptography.SHA256]::Create().ComputeHash([byte[]](0x30,0x59,0x30,0x13,0x06,0x07,0x2a,0x86,0x48,0xce,0x3d,0x02,0x01,0x06,0x08,0x2a,0x86,0x48,0xce,0x3d,0x03,0x01,0x07,0x03,0x42,0x00)+$c.GetPublicKey())) -eq $p}'

function curlFlags(o: CmdOptions): string {
  const pin = pinOf(o.info)
  return pin ? ` -k --pinnedpubkey "sha256//${pin}"` : ''
}

function policyArgs(o: CmdOptions, style: 'sh' | 'ps' | 'msi'): string[] {
  const keys = o.operatorKeys.length ? o.operatorKeys : [KEY_PLACEHOLDER]
  if (o.policy === 'none') return style === 'msi' ? ['WRITEJOBS=none'] : style === 'ps' ? ['-WriteJobs none'] : ['--write-jobs none']
  if (o.policy === 'operator') {
    if (style === 'msi') return ['WRITEJOBS=operator', `OPERATORKEY=${keys.join(',')}`]
    if (style === 'ps') return ['-WriteJobs operator', ...keys.map((k) => `-OperatorKey '${k}'`)]
    return ['--write-jobs operator', ...keys.map((k) => `--operator-key ${k}`)]
  }
  return []
}

/** The curl | sh one-liner. */
export function scriptCommand(o: CmdOptions): string {
  const pin = pinOf(o.info)
  const ca = caOf(o.info)
  const args = [`--server ${o.info.server_url}`, `--token ${o.token}`]
  if (ca) args.push(`--ca-fingerprint ${ca}`)
  if (pin) args.push(`--pin-sha256 ${pin}`)
  args.push(...policyArgs(o, 'sh'))
  if (o.docker) args.push('--docker')
  return `curl -fsSL${curlFlags(o)} ${o.info.server_url}/install/agent.sh | sudo sh -s -- ${args.join(' ')}`
}

/** The PowerShell one-liner (Windows PowerShell 5.1, run as Administrator). */
export function windowsCommand(o: CmdOptions): string {
  const pin = pinOf(o.info)
  const ca = caOf(o.info)
  const args = [`-Server ${o.info.server_url}`, `-Token ${o.token}`]
  if (ca) args.push(`-CaFingerprint ${ca}`)
  if (pin) args.push(`-PinSha256 ${pin}`)
  args.push(...policyArgs(o, 'ps'))
  const pre = pin ? `$p='${pin}'; ${PS_PIN_CALLBACK}; ` : ''
  return `${pre}[Net.ServicePointManager]::SecurityProtocol='Tls12'; & ([scriptblock]::Create((irm ${o.info.server_url}/install/agent.ps1))) ${args.join(' ')}`
}

function registerCommand(o: CmdOptions): string {
  const args = [`--server ${o.info.server_url}`, `--token ${o.token}`]
  const ca = caOf(o.info)
  if (ca) args.push(`--ca-fingerprint ${ca}`)
  args.push(...policyArgs(o, 'sh'))
  return `sudo sentrel-agent register ${args.join(' ')}`
}

/** Install a downloaded .deb or .rpm, register, start. */
export function packageCommand(o: CmdOptions, pkg: Pick<InstallPackage, 'file' | 'format'>): string {
  const install = pkg.format === 'deb' ? `sudo apt install ./${pkg.file}` : `sudo dnf install ./${pkg.file}`
  const steps = [install, registerCommand(o)]
  if (o.docker) steps.push('sudo usermod -aG docker sentrel-agent')
  steps.push('sudo systemctl enable --now sentrel-agent')
  return steps.join(' && ')
}

/** Download a package directly on the host (pinned in self-signed mode). */
export function fetchCommand(o: CmdOptions, pkg: Pick<InstallPackage, 'file'>): string {
  return `curl -fsSLO${curlFlags(o)} ${o.info.server_url}/download/agent/pkg/${pkg.file}`
}

export function msiCommand(o: CmdOptions, pkg: Pick<InstallPackage, 'file'>): string {
  const args = [`SERVER=${o.info.server_url}`, `TOKEN=${o.token}`]
  const ca = caOf(o.info)
  if (ca) args.push(`CAFINGERPRINT=${ca}`)
  args.push(...policyArgs(o, 'msi'))
  return `msiexec /i ${pkg.file} ${args.join(' ')} /qn`
}

export function findPackage(pkgs: InstallPackage[], platform: Platform, arch: Arch): InstallPackage | undefined {
  if (platform === 'script') return undefined
  const format = platform === 'windows' ? 'msi' : platform
  // Only an x64 MSI exists; Windows on ARM runs it under emulation.
  const a = platform === 'windows' ? 'amd64' : arch
  return pkgs.find((p) => p.format === format && p.arch === a)
}

/** The server URL points at this machine only, so other hosts probably cannot reach it. */
export function urlLooksWrong(serverUrl: string, browserHost: string): boolean {
  let host = ''
  try { host = new URL(serverUrl).hostname } catch { return true }
  const isLocal = (h: string) => h === 'localhost' || h === '::1' || h === '[::1]' || h.startsWith('127.')
  return isLocal(host) && !isLocal(browserHost)
}
