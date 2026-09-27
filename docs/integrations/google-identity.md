# Google Identity

Google Identity is the optional sign-in integration for managing personal scoped API keys. It does not collect transport data. The dashboard's configured public-read policy is independent of whether Google sign-in is available.

## Resources and configuration

| Boundary | Consumed resource/contract |
|---|---|
| Browser | `https://accounts.google.com/gsi/client`, initialized through `google.accounts.id` |
| Server | `google.golang.org/api/idtoken.Validate` for ID-token validation/signing-key retrieval |
| Application API | Configuration/login/account/key operations in [OpenAPI](../../api/openapi.yaml) |

[The server](../../internal/app/server.go) configures the default validator from the SDK version pinned in [go.mod](../../go.mod). Its signing-key retrieval is SDK-managed, separate from the shared transport-provider client. Configure `GOOGLE_CLIENT_ID` and exact `PUBLIC_ORIGIN`; production origin must be HTTPS. [The auth panel](../../frontend/src/AuthPanel.tsx) loads the script only when sign-in is needed and a client ID is configured, reusing the existing script/global when available.

In the pinned `google.golang.org/api` v0.299.0 validator, RS256 verification retrieves keys from `https://www.googleapis.com/oauth2/v3/certs`; its ES256 branch uses `https://www.gstatic.com/iap/verify/public_key-jwk`. These are SDK-selected verification resources, not extra application integration families. The application's explicit issuer/audience/claim checks still apply after SDK verification.

No periodic transport collection applies. Server validation has a ten-second context; signing-key caching follows the pinned SDK. Browser resource caching is not controlled by the transport cache.

## Inputs and normalized identity

| Input/claim | Application treatment | Result |
|---|---|---|
| Config `google_client_id` | Public browser initialization and server audience validation | Client identity, not a secret |
| Config `login_nonce` | Browser initialization; bound to login cookie/origin | Login replay/binding check |
| Callback/body `credential` | ID token passed to server; verified by SDK | Transient validated payload, not a transport/source record |
| Validated issuer | Must be `accounts.google.com` or `https://accounts.google.com` | Admitted provider identity |
| Validated audience | Must equal configured client ID | Correct application audience |
| Validated expiry | Must be later than server time | Unexpired token |
| Claim `nonce` | String matching valid browser login binding | Accepted login context |
| Claim `email_verified` | Boolean true required | Verified-email admission |
| Claim `email` | Nonempty string required | Session identity and API-key ownership |
| Claim `name` | Optional string | Account display name |
| Request `Origin`, login cookie | Must bind to configured origin and nonce | Accepted browser login |

Signature validation belongs to the SDK, followed by explicit [application claim checks](../../internal/app/auth.go). Other claims, including subject/profile attributes not read by `googleClaims`, do not populate a separate application profile. API identity/key ownership currently uses the admitted email.

## Session and key lifecycle

| Data | API/UI use | Durable storage | Transport history |
|---|---|---|---|
| Raw Google credential/validated payload | Login request and validation | No raw-token persistence by this login path | None |
| Email/name/auth kind/expiry | Own account and key management | Session row | None |
| Session secret | HttpOnly `lp_session` cookie | SHA-256 hash in `sessions`, not raw secret | None |
| Login nonce | Short-lived login binding | Browser cookie/config exchange | None |
| API-key identity/name/scopes/expiry/revocation | Own key list and API authorization | `api_keys` plus hashed key secret | None |
| Newly created API-key secret | Shown once in the auth panel | Hash only | None |

Sessions last 24 hours. Cookies use HttpOnly, SameSite policy and Secure for HTTPS origins. Logout removes the session and clears the cookie. API keys have `read:transit`/`read:history` scopes, bounded ownership/count and expiry; key management requires a session. Supplied keys must still pass checks on public reads. Exact operation contracts remain in OpenAPI.

## Missing and invalid data

Absent client configuration makes sign-in unavailable; the UI can still use configured public reads. Invalid origin/nonce/signature/issuer/audience/expiry or unverified/missing email prevents session creation. A valid token with absent display name can still produce an empty name. Local development login is a separate restricted application path, not a Google provider identity.

Synthetic example: a verified ID token for a different client audience is rejected; an admitted email produces a session whose stored credential is the hash of a newly generated session secret, not the Google token.

## Evidence

Implementation: [AuthPanel](../../frontend/src/AuthPanel.tsx), [server validator](../../internal/app/server.go), [auth claims/sessions/keys](../../internal/app/auth.go), [schema](../../internal/app/store.go). Existing evidence: [security tests](../../internal/app/security_fixes_test.go), [app authentication tests](../../internal/app/app_test.go), [dated verification/setup sources](../research/SOURCES.md#reference-and-setup-sources).
