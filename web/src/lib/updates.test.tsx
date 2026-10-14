import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ReleaseNotes } from '@/components/ReleaseNotes'
import { dismiss, installHelp, isActivePhase, isDismissed, isFinalPhase, parseInline, parseNotes, showBanner, showPill, stepIndex, STEPS, type UpdateInfo } from './updates'

const info = (o: Partial<UpdateInfo> = {}): UpdateInfo => ({ current: '0.9.1', latest: '0.9.2', update_available: true, check_enabled: true, ...o })
const memStore = () => { const m = new Map<string, string>(); return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) } }

describe('pill and banner', () => {
  it('pill is for admins with an update only', () => {
    expect(showPill(info(), true)).toBe(true)
    expect(showPill(info(), false)).toBe(false)
    expect(showPill(info({ update_available: false }), true)).toBe(false)
    expect(showPill(info({ latest: undefined }), true)).toBe(false)
    expect(showPill(undefined, true)).toBe(false)
  })
  it('banner is shown once per version', () => {
    const s = memStore()
    expect(showBanner(info(), true, s)).toBe(true)
    expect(showBanner(info(), false, s)).toBe(false)
    dismiss('0.9.2', s)
    expect(isDismissed('0.9.2', s)).toBe(true)
    expect(showBanner(info(), true, s)).toBe(false)
    expect(showBanner(info({ latest: '0.9.3' }), true, s)).toBe(true) // a newer version comes back
  })
  it('survives unavailable storage', () => {
    const broken = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } }
    expect(isDismissed('0.9.2', broken)).toBe(false)
    expect(() => dismiss('0.9.2', broken)).not.toThrow()
    expect(showBanner(info(), true, broken)).toBe(true)
    expect(showBanner(info(), true, null)).toBe(true)
  })
})

describe('phases', () => {
  it('orders the steps', () => {
    expect(stepIndex('downloading')).toBe(0)
    expect(stepIndex('checking')).toBe(STEPS.length - 1)
    expect(stepIndex('done')).toBe(STEPS.length)
    expect(isActivePhase('restarting')).toBe(true)
    expect(isActivePhase('rolling_back')).toBe(true)
    expect(isActivePhase('idle')).toBe(false)
    expect(isFinalPhase('rolled_back')).toBe(true)
    expect(isFinalPhase('verifying')).toBe(false)
  })
})

describe('release notes', () => {
  it('parses headings, lists and paragraphs', () => {
    const b = parseNotes('## What changed\n- one\n- **two** and `code`\n\nA paragraph\nwrapped.\n\n1. first\n2. second\n')
    expect(b.map((x) => x.t)).toEqual(['h', 'ul', 'p', 'ol'])
    expect((b[1] as any).items).toHaveLength(2)
    expect((b[2] as any).inline[0].text).toBe('A paragraph wrapped.')
  })
  it('links only http(s)', () => {
    const ok = parseInline('see [docs](https://example.com/a) and https://github.com/x/y/pull/1')
    expect(ok.filter((p) => p.t === 'link')).toHaveLength(2)
    const bad = parseInline('[x](javascript:alert(1)) [y](data:text/html,hi)')
    expect(bad.some((p) => p.t === 'link')).toBe(false)
  })
  it('escapes HTML instead of interpreting it', () => {
    const evil = '# <img src=x onerror=alert(1)>\n<script>alert(1)</script>\n- <b onclick="x()">bold</b>\n[click](javascript:alert(1))\n[ok](https://example.com)'
    const html = renderToStaticMarkup(<ReleaseNotes text={evil} />)
    expect(html).not.toContain('<script')
    expect(html).not.toContain('<img')
    expect(html).not.toContain('<b ')
    expect(html).not.toContain('href="javascript')
    expect(html).toContain('&lt;script&gt;')
    expect(html).toContain('&lt;img')
    expect(html).toContain('href="https://example.com"')
    expect(html).toContain('rel="noopener noreferrer"')
  })
  it('handles empty notes', () => {
    expect(renderToStaticMarkup(<ReleaseNotes text="" />)).toContain('no notes')
  })
})

describe('install help', () => {
  it('gives exact commands per method', () => {
    expect(installHelp('docker', '0.9.2').steps[0].code).toBe('docker pull ghcr.io/xedigital/sentrel:0.9.2')
    expect(installHelp('docker', '0.9.2').steps[1].code).toBe('docker compose up -d')
    expect(installHelp('package', '0.9.2').steps.map((s) => s.code).join('\n')).toContain('apt install --only-upgrade sentrel-server')
    expect(installHelp('package', '0.9.2').steps.map((s) => s.code).join('\n')).toContain('dnf upgrade sentrel-server')
    expect(installHelp('manual', '0.9.2').title).toContain('manually')
  })
})
