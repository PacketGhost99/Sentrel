# Status pages

A status page shows chosen hosts and checks to people who have no account. Create one under **Status pages** in the UI.

- **Title and URL slug**: the page is served at `/status/<slug>`.
- **Public**: on, anyone with the link can view it. Off, only people with the secret link (it carries a token) can.
- **Items**: pick hosts and checks and give each a display label. Only labels and statuses are published, never hostnames, addresses or metrics.
- **90-day history**: optionally show daily uptime for each item.
- **Announcement banner**: optional text, for example a maintenance notice.

The data endpoint for public pages is described in the [API](api.md#public-status-pages).
