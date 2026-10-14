import {mkdir,writeFile,readFile} from 'node:fs/promises';
const root=new URL('../dist/',import.meta.url), repo='PacketGhost99/Sentrel',raw=`https://raw.githubusercontent.com/${repo}/main/`;
async function get(url,json=false){const r=await fetch(url,{headers:{'User-Agent':'Sentrel-Website'},signal:AbortSignal.timeout(20000)});if(!r.ok)throw new Error(`${url}: ${r.status}`);return json?r.json():r.text();}
const index=await get(raw+'docs/README.md');
const paths=new Set(['docs/README.md','README.md','CHANGELOG.md','SECURITY.md','CONTRIBUTING.md']);
for(const m of index.matchAll(/\]\(([^)]+\.md)(?:#[^)]*)?\)/g)){if(!m[1].startsWith('http'))paths.add(new URL(m[1],'https://repo.local/docs/').pathname.slice(1));}
const docs={};await Promise.all([...paths].map(async p=>docs[p]=await get(raw+p)));
const r=await get(`https://api.github.com/repos/${repo}/releases/latest`,true);
const release={tag_name:r.tag_name,name:r.name,published_at:r.published_at,html_url:r.html_url,body:r.body,assets:r.assets.map(a=>({name:a.name,size:a.size,browser_download_url:a.browser_download_url}))};
await writeFile(new URL('snapshot.json',root),JSON.stringify({synced_at:new Date().toISOString(),repo,branch:'main',docs,release}));
await mkdir(new URL('assets/',root),{recursive:true});
await Promise.all([['marked.js','https://cdn.jsdelivr.net/npm/marked@15.0.12/marked.min.js'],['purify.js','https://cdn.jsdelivr.net/npm/dompurify@3.3.0/dist/purify.min.js']].map(async([file,url])=>writeFile(new URL('assets/'+file,root),await get(url))));
console.log(`Synced ${paths.size} documents and ${release.assets.length} release assets (${release.tag_name}).`);
