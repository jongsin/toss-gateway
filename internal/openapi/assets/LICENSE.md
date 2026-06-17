# Vendored third-party assets — Swagger UI

This directory contains **Swagger UI** static distribution files, vendored (self-hosted)
into the gateway binary via `go:embed` to eliminate the runtime dependency on an external
CDN (see security finding **SEC-04**).

| File | Source |
|------|--------|
| `swagger-ui.css` | `https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css` |
| `swagger-ui-bundle.js` | `https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js` |

- **Project**: Swagger UI — https://github.com/swagger-api/swagger-ui
- **Version**: `5.17.14` (pinned)
- **License**: Apache License 2.0
- **Copyright**: © SmartBear Software and Swagger UI contributors

The files are redistributed unmodified. The Apache-2.0 license text is available at
https://www.apache.org/licenses/LICENSE-2.0 and in the upstream project's `LICENSE` file.
Per the bundle header, per-dependency license notices are catalogued upstream in
`swagger-ui-bundle.js.LICENSE.txt`.

> Upgrade procedure: replace both files with the matching version from the same CDN path,
> bump the version above and in `internal/openapi/openapi.go` / `swagger.html`, then
> rebuild so the new bytes are re-embedded.
