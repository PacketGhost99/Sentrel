# Sentrel public website

A portable, buildless marketing website, download catalogue and documentation reader.

## Preview

Run `node scripts/serve.mjs` and open http://127.0.0.1:4173.

## GitHub content

The website reads the latest non-prerelease release from GitHub's releases API and uses its actual asset URLs. Component and platform filters never construct guessed package names.

Documentation is sourced from `docs/README.md` on the repository's `main` branch. Links in that index build the navigation; Markdown pages are fetched directly from GitHub when visited. Markdown is parsed with vendored Marked and sanitized with DOMPurify. Relative documentation links stay in the reader; other repository links open GitHub. Main-branch documentation can describe changes newer than the latest release.

Browser responses are cached for five minutes. If GitHub is unavailable or rate-limits a visitor, a cached copy or the deployment snapshot is displayed with its source status. No token or connector sign-in is required. New release assets and doc changes appear without redeploying; marketing copy remains deliberately editorial.

Run `node scripts/sync.mjs` before publication to refresh the bundled fallback from public GitHub, along with pinned Markdown libraries. No scheduled task is required for normal live refresh. Updating the fallback requires a new deployment. For stronger caching and indexing of every documentation page, a future server-rendered build could be added.

The website is separate from the Sentrel application. No changes to its runtime or existing GitHub workflows are required. Source is in this directory and can also be hosted by any static web host. Sites identity lives in `.openai/hosting.json`.


## Licensing
The website reflects Sentrel's existing Apache License 2.0: free personal and commercial use. No paid software tiers, commercial subscription or pricing calculator remains. XEDIGITAL LTD ownership and contact information are retained. The application LICENSE was already Apache 2.0 and has not been changed by website edits.
