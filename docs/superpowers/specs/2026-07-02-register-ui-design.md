# Design Specification: User Registration and Email Verification

This specification describes the architecture, database changes, user interface pages, and backend routing endpoints required to implement user registration with email verification on Nacfson Cloud.

## 1. Database Modifications
To keep track of user verification status and validation tokens, the Postgres schema is modified as follows:

```sql
-- 1. Add verification status column to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_verified BOOLEAN NOT NULL DEFAULT false;

-- 2. Create table for storing email verification tokens
CREATE TABLE IF NOT EXISTS email_verifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## 2. User Interfaces
Two new Go templates will be added under `web/templates`:

### A. `register.html`
* Stylistically identical to the glassmorphic dark theme of `login.html`.
* **Form Inputs**:
  * Email Address
  * Password
  * Password Confirmation
  * `return_to` (hidden field containing the redirect destination)
* **Form Submission**: `POST /register`
* Displays error messages passed via query parameter (e.g. `?error=passwords+do+not+match`).

### B. `verification_sent.html`
* Renders dynamically based on whether SMTP is configured.
* If SMTP is enabled: Displays instructions to check the email inbox.
* If SMTP is disabled (local/development): Displays the exact activation link directly on the screen for instant local testing.

## 3. Route Handlers & Endpoint Flows
The following new routes will be added to the auth server in `internal/auth/handler.go`:

### `GET /register`
* Parses `rd` parameter to forward as redirect destination.
* Renders `register.html` template.

### `POST /register`
* Parses form parameters.
* Validates that both passwords match and the email is valid.
* Checks if a user already exists with that email. If so, redirects to `/register?error=email already registered`.
* Hashes the password with `bcrypt`.
* Calls `store.CreateUser` (passing `is_verified = false`).
* Generates a 32-character random secure token.
* Saves token to `email_verifications` with a 24-hour expiration duration.
* Attempts to send a verification email with link `http://<domain>/verify-email?token=<token>&rd=<redirect_url>`.
  * If SMTP configurations (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM`) are not set, it prints the link to server logs/stdout.
* Redirects user to `/verification-sent?token=<token>&rd=<redirect_url>`.

### `GET /verify-email`
* Fetches the token from the query parameters.
* Resolves the token in `email_verifications`. If not found or expired, shows an error.
* Updates the user to `is_verified = true`.
* Creates a login session for the user and sets the session cookie (auto-login).
* Deletes the used verification token.
* Redirects the user to the original target page (`rd` or `return_to`).

### `POST /login` Update
* Ensure that logging in checks `user.IsVerified`. If false, rejects authentication with `error=email not verified`.
* *Note: Bootstrapped admin accounts automatically default to `is_verified = true` so administrative access remains uninterrupted.*
