import { chromium } from 'playwright';
import { readFileSync, existsSync } from 'node:fs';
const base = 'http://127.0.0.1:8080';
const user = process.env.HB_DEMO_USER, pass = process.env.HB_DEMO_PASSWORD;
// Text to mask before each screenshot, e.g. real hostnames or public IPs.
// Read from shots-masks.json next to this script (git-ignored), as
// [["regex source", "flags", "replacement"], ...]. Never commit real values.
const maskFile = new URL('./shots-masks.json', import.meta.url);
const mask = existsSync(maskFile)
  ? JSON.parse(readFileSync(maskFile, 'utf8')).map(([src, flags, rep]) => [new RegExp(src, flags), rep])
  : [];
async function scrub(page) {
  await page.evaluate((pairs) => {
    const res = pairs.map(([s, f, r]) => [new RegExp(s, f), r]);
    const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n; (n = w.nextNode());) for (const [re, r] of res) n.nodeValue = n.nodeValue.replace(re, r);
    document.querySelectorAll('[title],input').forEach(e => { for (const [re, r] of res) { if (e.title) e.title = e.title.replace(re, r); if (e.value) e.value = e.value.replace(re, r); } });
  }, mask.map(([re, r]) => [re.source, re.flags, r]));
}
const b = await chromium.launch();
for (const theme of ['light', 'dark']) {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: theme, locale: 'en-GB', timezoneId: 'Europe/London' });
  const page = await ctx.newPage();
  await page.addInitScript(t => localStorage.setItem('sentrel-theme', t), theme);
  await page.goto(base + '/login');
  await page.fill('input[autocomplete="username"], input[name="username"], input[type="text"]', user);
  await page.fill('input[type="password"]', pass);
  await page.click('button[type="submit"]');
  await page.waitForURL(u => !u.pathname.startsWith('/login'), { timeout: 15000 });
  const pages = theme === 'light'
    ? [['dashboard', '/'], ['hosts', '/hosts'], ['host', '/hosts/1'], ['checks', '/checks'], ['vulnerabilities', '/vulnerabilities']]
    : [['dashboard', '/'], ['host', '/hosts/1']];
  for (const [name, path] of pages) {
    await page.goto(base + path); await page.waitForLoadState('domcontentloaded'); await page.waitForTimeout(4000);
    await scrub(page);
    const txt = await page.evaluate(() => document.body.innerText);
    const leak = mask.find(([re]) => new RegExp(re.source, re.flags).test(txt));
    if (leak) { console.log('LEAK on', name, theme, leak[0]); continue; }
    await page.screenshot({ path: `/out/${name}-${theme}.png` });
    console.log('saved', name, theme);
  }
  await ctx.close();
}
await b.close();
