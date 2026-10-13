# UI scripts

Headless browser helpers for the web UI, run with the official Playwright image against a server you control:

```sh
docker run --rm --network host -e HB_DEMO_USER=... -e HB_DEMO_PASSWORD=... \
  -v "$PWD/scripts/ui:/work" -v "$PWD/docs/screenshots:/out" -w /work \
  mcr.microsoft.com/playwright:v1.56.1-noble sh -c "npm i playwright@1.56.1 && node shots.mjs"
```

- `shots.mjs` captures the README screenshots (light and dark) into `/out`. It refuses to save a page that still shows any text listed in the git-ignored `shots-masks.json`.
- `crawl.mjs` opens every page and every host tab and reports pages that fail to render. Set `U` and `P` to an admin login.
