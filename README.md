# Facets Server

Go backend for personal and shared financial tracking. This repository contains
the server; the Android client is developed separately.

## Documentation

- [Local operation](docs/LOCAL_DEVELOPMENT.md)
- [Bruno API collection](api-client/README.md)
- [Product design](docs/producto/PRODUCT_DESIGN.md)
- [Technical design](docs/producto/TECHNICAL_DESIGN.md)
- [Development plan](docs/producto/DEVELOPMENT_PLAN.md)

Essential checks: `./scripts/dev.ps1 check` and `./scripts/dev.ps1 validate`.
Check `api-client/` with `./scripts/dev.ps1 api-client-check`; run its optional
local Bruno smoke with `./scripts/dev.ps1 api-client-smoke`.
