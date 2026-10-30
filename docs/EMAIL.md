# Amazon SES mail infrastructure

`internal/mail` replaces the previous mail transport completely. It uses Amazon
SES **API v2** (`SendEmail`) through **AWS SDK for Go v2**. Module versions are
pinned in `go.mod`/`go.sum`; no mail transport dependency other than the official
SDK is needed. There are no verification, registration, recovery or notification
use cases, mail endpoints, or permissive worker handlers in this change.

## Composition and configuration

When a business module needs mail, construct one reusable sender with
`mail.LoadSES(ctx, mail.Options{From: verifiedAddress, Timeout: 10*time.Second})`.
The caller supplies the verified sender explicitly; it is not controlled by a
request or queued message. Initialization validates the sender and finite timeout.
The SDK resolves the region and credentials using its native chain, including
`AWS_REGION`, shared config/`AWS_PROFILE`, and workload roles. Missing region fails
initialization. Credential retrieval can be lazy; successful construction is not
proof of AWS access or identity verification.

Prefer temporary workload-role credentials in production and an authenticated
development profile locally. Never place access keys in source, `.env.example`,
queued payloads or logs. No manual signing, credential storage or endpoint
selection is implemented. `NewSES` accepts the narrow SDK interface for tests.
It applies the same operation-level safe retry policy even to injected clients.

The current executable has no mail consumers, so it deliberately does not load
AWS configuration or create an unused client. Database/HTTP/worker startup and
tests need neither AWS credentials nor network access to AWS. Introduce runtime
sender configuration at the composition root when the first authorized business
module is added; do not initialize mail per HTTP request.

## Messages and templates

`Send(ctx, Message)` sends already-rendered content. `To` is one mailbox; optional
`Cc`, `Bcc` and `ReplyTo` are mailbox lists. One sender is fixed in `Options`.
`Subject`, `Body` (plain text), and `HTML` use UTF-8; at least one body is required.
Each address must be a bare ASCII mailbox, with punycode domains if needed.
Display-name syntax is intentionally excluded. Recipient totals are limited to
50; reply-to addresses to 10; subject to 200 bytes; combined bodies to 60,000 bytes.
Malformed addresses, header injection, NULs and invalid UTF-8 are rejected before
calling SES. These are application safety budgets, not claims about SES maxima.
The durable `Deliver` adapter additionally limits serialized payloads to 65,536
bytes, preserving the existing JSON `to`, `subject`, `body` contract.

`NewTemplate(subject, text, html)` parses trusted application-owned templates once.
`Render(data)` returns a `Message` containing content only. The caller sets the
authorized recipient afterwards. Missing map keys and oversized rendered parts
fail closed. HTML uses `html/template` automatic escaping; plain text/subjects use
`text/template`. Never accept template source or trusted `template.HTML` values
from users. The same parsed template can be rendered concurrently. Rendered
messages still pass all send-time validation.

Templates stay local: there is no SES template provisioning, remote rendering,
configuration set, dedicated IP, global endpoint or additional AWS service.

## Future durable handler integration

Use the existing transactional outbox, not a second queue:

1. Enqueue a domain-specific job in the transaction that creates the relevant fact.
   Set `ExternalEffect: true` and a stable `DedupKey` for the logical communication.
   Repeating identical admission returns the same job; changed content conflicts.
2. Register a specific `execution.Handler` with `Check` that revalidates recipient,
   token/resource validity, authorization and preferences immediately before send.
   Do not register a generic public arbitrary-recipient sender.
3. Use `sender.Deliver` as `Execute` for a validated, rendered `Message` payload,
   or a handler that prepares fresh content and calls `sender.Send`.
4. Keep the mail timeout within the worker timeout; retain time for durable
   completion within the lease. Preserve existing job payload/result retention.

Do not enqueue long-lived plaintext passwords or log codes, tokens, links, message
subjects, bodies or address lists. Prefer minimal references and render secrets
near send time when the future business workflow permits it. Payload storage is
not a secrets vault. This adapter validates format, **not recipient authorization**.

## Reliability and diagnostics

`SendEmail` has no idempotency token. Neither the job ID nor the returned SES
message ID provides provider deduplication. A successful call means SES accepted
the message for processing, not mailbox delivery or inbox placement.

- The SDK standard retryer supplies jitter/backoff and retry limits, with at most
  three attempts **only for explicit `TooManyRequestsException` rejections**.
  Default retries for network failures and server errors are deliberately excluded.
- Explicit quota rejections are safe failures; the outbox applies its bounded
  retry/backoff policy after SDK attempts are exhausted. Tune worker concurrency
  and admission against the account's regional sending quotas; there is no
  unnecessary distributed rate-limiter service. Sustained quota exhaustion needs
  operator action, not an unbounded retry loop.
- Invalid messages and explicit permanent client/authorization/identity failures
  match `execution.ErrPermanent`; workers mark them failed immediately. Operators
  can correct configuration and deliberately retry failed jobs where appropriate.
- Lost responses, server failures, timeouts/cancellation after SDK invocation, and
  missing acceptance IDs conservatively match `execution.ErrUncertain`. The worker
  persists uncertainty and never automatically resends. Some pre-network SDK
  failures are also conservatively uncertain when no safe rejection is available.
- Cancellation before calling the SDK preserves the context error without
  reporting an external effect. Underlying causes remain available through
  `errors.Is`/`errors.AsType`, including AWS typed errors and context cancellation.

`DeliveryError.Error()` omits AWS's potentially sensitive error message. Safe
`ErrorCode()` and `RequestID()` diagnostics are recorded by the existing
`telemetry.LogError` boundary, without logging raw causes. Existing worker metrics
and audit events track completion/failure/uncertainty, age and duration. The SES
acceptance ID is stored as `result_ref`. Do not enable SDK request/response-body
logging or call `Unwrap().Error()` in production logs.

## AWS prerequisites (operator-owned, not automatically provisioned)

Before enabling a real mail use case, verify its sending identity in the selected
region, configure DKIM and appropriate SPF/DMARC/MAIL FROM records, and grant the
workload only `ses:SendEmail` on the identity ARN covering its From address.
For example, the resource is
`arn:aws:ses:<region>:<account-id>:identity/<verified-domain>`; do not grant `ses:*`
or administrator access. No account reads or provisioning permissions are needed
by the runtime sender. Region, identity and permissions must agree.

In the SES sandbox, recipients must be (1) individually verified addresses,
(2) SES mailbox simulator addresses, or (3) addresses at any verified domain in
the account. A verified sender is required both inside and outside the sandbox.
Production access is a separate operator-approved AWS process.

Bounce/complaint handling, suppression monitoring and delivery-event processing
must be decided before real production use cases launch. API acceptance alone
cannot measure deliverability. No event infrastructure or paid optional features
are silently provisioned. Account settings may already enable additional features
and charges; review them deliberately. HTTPS protects the API connection, not an
end-to-end guarantee for the message. Delivery TLS is opportunistic by default;
requiring recipient-server TLS needs an explicitly managed SES configuration set.

## Verification

Unit tests cover request construction, validation, templates/HTML escaping,
contexts, safe SDK retries, AWS typed errors, diagnostic redaction and HTTPS signing.
Integration tests send through the real SDK to a local TLS fixture and persist
accepted/uncertain results in PostgreSQL, including no resend after a dropped
acknowledgement. No real emails are sent and no cloud resources are created.

```powershell
go test ./internal/mail ./internal/telemetry ./internal/execution
./scripts/dev.ps1 integration
./scripts/dev.ps1 validate
```

References: [SES SendEmail API](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_SendEmail.html),
[Go v2 SDK configuration](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html),
[SES sending errors](https://docs.aws.amazon.com/ses/latest/dg/troubleshoot-error-messages.html).
