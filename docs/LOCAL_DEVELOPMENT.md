# Local operation

Use PowerShell 7.4+ and Docker with Linux containers. From the repository root:

```powershell
./scripts/dev.ps1 setup
./scripts/dev.ps1 db-up
./scripts/dev.ps1 db-config
```

Start the API and notification worker in separate terminals:

```powershell
./scripts/dev.ps1 run
```

```powershell
./scripts/dev.ps1 worker
```

The API listens on `127.0.0.1:8080`. With the generated configuration,
diagnostics (`GET /metrics`) use `127.0.0.1:9090` for HTTP and
`127.0.0.1:9091` for the worker. No `.env` edits are needed. The script preserves
a custom `.env` diagnostics address or an explicit session override, including
`$env:FACETS_DIAGNOSTICS_ADDRESS = '127.0.0.1:9191'`. Choose distinct addresses
when overriding defaults or starting additional workers. Direct binary execution
does not apply these local script defaults.

## Mail

With `FACETS_ENV=development` and an empty `FACETS_MAIL_FROM`, the notification
worker does not load AWS configuration or require AWS credentials. It writes each
rendered message to its structured log as `development mail delivery`, with
`message.to`, `message.subject` and `message.body` (including the confirmation or
invitation code). A startup warning identifies this mode.

This uses the normal durable job handler: authorization, expiry, invalidation,
delivery-secret cleanup and job completion remain in place. Registration,
verification, password recovery, email changes, invitations and security notices
all use this same delivery path. Read the relevant code from the worker log and
submit it to the corresponding API operation; see [OpenAPI](openapi.json) for
request bodies. Registration can be completed with `POST /v1/auth/verify`, then
`POST /v1/auth/login`.

**Development mail logs contain secrets and recipient addresses.** Use synthetic
data, keep logs local, do not forward them to shared logging systems, and delete
captured logs after debugging. Normal application logs must not contain codes.

Setting `FACETS_MAIL_FROM` selects real SES delivery even in development. Configure
an authorized sender, AWS region and credentials through the native AWS SDK
configuration chain. SES errors never fall back to logging.

In `production` and `test`, a notification worker without a valid sender fails
explicitly. Real deployments require SES configuration and permissions; log
delivery is unavailable. HTTP and heavy workers do not require a mail sender.
Automated integration tests use isolated PostgreSQL and synthetic mail delivery,
not a live AWS account.

## Verification

`./scripts/dev.ps1 check` is the fast local check. Before closing changes, run
`./scripts/dev.ps1 validate` for unit/integration tests, script tests, static
analysis, race detection, generated-file checks, vulnerability scanning and build.
Integration tests use temporary databases, not the local development database.
