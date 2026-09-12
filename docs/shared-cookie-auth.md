# Shared Cookie Authentication

Project PN uses the central auth service for browser SSO. The browser is
redirected to `https://auth.nacfson.cloud/login?rd=https://pn.nacfson.cloud`;
after the email/password flow completes, Auth sets the parent-domain
`nacfson_session` cookie and redirects back to PN.

The cookie is:

- `Domain=nacfson.cloud`
- `Path=/`
- `Secure`
- `HttpOnly`
- `SameSite=Lax`

Project PN's SPA shell remains public. Its backend must authenticate protected
`/api` requests by forwarding the incoming `nacfson_session` cookie to the
internal Auth endpoint:

```http
GET /api/internal/cookie-session
Authorization: Bearer <AUTH_SESSION_INTERNAL_TOKEN>
Cookie: nacfson_session=<browser-cookie>
```

The response is the central identity:

```json
{
  "user_id": "uuid",
  "email": "user@example.com",
  "is_admin": false,
  "expires_at": "2026-07-31T12:00:00Z"
}
```

The raw session cookie is hashed inside Auth and is never placed in a query
string or returned to browser JavaScript. Project PN must reject client-supplied
`X-User-*` headers and derive identity only from this validation response.

Native clients use the bearer-token API. For browser handoff, Auth may redirect to an
allow-listed custom scheme (for example `nacfsonhealth://login-callback`) with
`?token=<opaque-session>` instead of relying on the Secure cookie. Configure
`AUTH_ALLOWED_REDIRECTS` accordingly. HTTPS sibling apps continue to receive the
`nacfson_session` cookie only.

Browser mutations must use Project PN's / health-web's host-only CSRF token and
validate the `Origin` header; `SameSite=Lax` is not sufficient protection between
sibling subdomains.

`AUTH_SESSION_INTERNAL_TOKEN` is a separate secret from
`AUTH_INTERNAL_TOKEN`. It grants only cookie-session validation and must never
be included in frontend configuration or JavaScript bundles.
