import { chromium } from 'playwright';
const base = 'http://127.0.0.1:8080';
const b = await chromium.launch(); const ctx = await b.newContext({ locale: 'en-GB', viewport: { width: 1440, height: 900 } }); const p = await ctx.newPage();
const errs = []; let where = 'login';
p.on('pageerror', e => errs.push(`${where}: ${e.message}`));
await p.goto(base + '/login');
await p.fill('input[autocomplete="username"]', process.env.U); await p.fill('input[type="password"]', process.env.P);
await p.click('button[type="submit"]'); await p.waitForTimeout(2500);
const routes = await p.evaluate(() => [...new Set([...document.querySelectorAll('nav a[href^="/"]')].map(a => a.getAttribute('href')))]);
async function check(label) { await p.waitForTimeout(1800); const t = await p.evaluate(() => document.body.innerText); if (/failed to render/i.test(t)) errs.push(`${label}: ${t.match(/This page failed to render[^\n]*/)?.[0]}`); }
for (const r of routes) { where = r; await p.goto(base + r); await check(r); }
for (const id of [1, 2, 8]) {
  where = `/hosts/${id}`; await p.goto(`${base}/hosts/${id}`); await check(where);
  const tabs = await p.evaluate(() => [...document.querySelectorAll('[role="tab"], [role="tablist"] button')].map(e => e.textContent.trim()).filter(Boolean));
  for (const t of tabs) { where = `/hosts/${id} tab ${t}`; await p.getByRole('tab', { name: t }).first().click().catch(async () => { await p.locator('button', { hasText: t }).first().click().catch(() => {}); }); await check(where); }
  console.log(`host ${id} tabs: ${tabs.join(' | ')}`);
}
console.log('routes:', routes.join(' '));
console.log(errs.length ? 'ERRORS:\n' + errs.join('\n') : 'NO ERRORS');
await b.close();
