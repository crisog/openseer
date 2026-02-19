# OpenSeer Web

Next.js frontend for OpenSeer dashboards and monitor management.

## Development

```bash
npm run dev
```

App runs on `http://localhost:3000`.

## Scripts

```bash
npm run dev            # next dev --turbopack
npm run build          # next build --turbopack
npm run start          # next start
npm run auth:generate  # regenerate Better Auth schema file
npm run auth:migrate   # apply Better Auth DB migrations
npm run gen:proto      # run buf generate from repository root
```

## Notes

- Fonts are loaded via `next/font/google` and the app currently uses `JetBrains_Mono` in `web/app/layout.tsx`.
- RPC transport/query wiring is centralized under `web/lib/api/` and `web/components/providers/`.
