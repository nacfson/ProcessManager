# Spec: nacfson_health Web on ProcessManager

**Date:** 2026-09-09
**Status:** Approved — Option B + mobile mockup v2 (navy/mint, Korean, tight density). Implementation not started.
**Related:** [shared-cookie-auth.md](./shared-cookie-auth.md), Flutter `docs/web-k8s-integration-options.html`, Flutter `docs/db-schema.md`

---

## 1. Decisions (locked)

| Decision | Choice |
|----------|--------|
| Product split | **Web** = new ProcessManager app. **Native Flutter iOS/Android** = keep current SQLite + Supabase + Google OAuth (**origin unchanged**). |
| Auth | ProcessManager central auth only (`nacfson_session` on `Domain=nacfson.cloud`). |
| Auth edge | **PN-style**: public UI shell; **API** validates session via `GET /api/internal/cookie-session` + `AUTH_SESSION_INTERNAL_TOKEN`. |
| Data | ProcessManager-provisioned Postgres in `nacfson-data` (not Supabase for web). |
| UI stack | Mobile-first web (max-width ~600px). Prefer HTMX + Go templates *or* light SPA — pick at scaffold time; contract below is API-first so either works. |
| Sync bridge | **Later optional** — not in MVP. Native and web may diverge until bridge ships. |

Non-goals for MVP:

- HealthKit / Health Connect / Live Activities on web  
- Replacing or breaking the Flutter store builds  
- Direct browser access to Auth DB or shared-app admin credentials  
- Trusting client-supplied `X-User-*` headers  

---

## 2. Hosting topology

```
https://health.nacfson.cloud          → frontend (static or Go-rendered)
https://health.nacfson.cloud/api/*    → health-api Deployment
https://auth.nacfson.cloud/login?rd=… → SSO entry
Postgres                              → provisioned DB `health` on shared-app-db.nacfson-data
```

Suggested K8s layout (mirror `k8s/project-pn/`):

```
k8s/health/
  namespace.yaml
  frontend.yaml + frontend-service.yaml
  backend.yaml  + backend-service.yaml
  ingress.yaml          # host: health.nacfson.cloud — NO auth-url on / (public shell)
  secret.yaml           # DATABASE_URL, AUTH_SESSION_INTERNAL_TOKEN, CENTRAL_AUTH_URL, CSRF secrets
  migrate-job.yaml      # optional one-shot schema migrate
```

Ingress notes:

- TLS via existing cert-manager issuer pattern.  
- Do **not** put `nginx.ingress.kubernetes.io/auth-url` on the whole host (that is portfolio/manager style).  
- Optional later: protect only `/admin` with ingress auth if needed.

Login redirect (browser):

```
https://auth.nacfson.cloud/login?rd=https://health.nacfson.cloud/
```

---

## 3. Authentication contract (API)

Follow [shared-cookie-auth.md](./shared-cookie-auth.md) exactly.

Cookie set by Auth (not by health-api):

- `Name=nacfson_session`
- `Domain=nacfson.cloud`
- `Path=/`
- `Secure`
- `HttpOnly`
- `SameSite=Lax`

`AUTH_SESSION_INTERNAL_TOKEN` is **separate** from `AUTH_INTERNAL_TOKEN` — it grants only cookie-session validation and must never ship in frontend bundles.

### 3.1 Validate session (server-side only)

```http
GET {CENTRAL_AUTH_URL}/api/internal/cookie-session
Authorization: Bearer {AUTH_SESSION_INTERNAL_TOKEN}
Cookie: nacfson_session=<incoming browser cookie>
```

Success `200`:

```json
{
  "user_id": "uuid",
  "email": "user@example.com",
  "is_admin": false,
  "expires_at": "2026-09-09T12:00:00Z"
}
```

Rules:

1. Health API copies the request’s `nacfson_session` cookie to Auth; never accepts user id from query/body/headers from the browser.  
2. `AUTH_SESSION_INTERNAL_TOKEN` stays in K8s Secret — never in frontend JS.  
3. Unauthenticated API → `401` with `{ "error": "unauthorized" }`. Frontend redirects to Auth login with `rd`.  
4. Browser mutations require host-only CSRF cookie + `Origin` check (sibling-subdomain rule from shared-cookie-auth).  
5. Optional native bearer tokens are out of scope for web MVP.

### 3.2 Frontend session bootstrap

```http
GET /api/me
Cookie: nacfson_session=…
```

`200` → `{ "user_id", "email" }`  
`401` → trigger login redirect

---

## 4. Database schema (web Postgres)

Source of truth for columns: Flutter `docs/db-schema.md` (v12 semantics), adapted for server-owned multi-tenant rows.

### 4.1 Principles

- Every user-owned row has `user_id UUID NOT NULL` (= Auth `user_id`).  
- Global PK = `uuid UUID PRIMARY KEY` (or `TEXT` storing UUID v4 — pick one and stay consistent; prefer `UUID`).  
- Soft delete: `is_deleted BOOLEAN NOT NULL DEFAULT false`.  
- Timestamps: `timestamptz` (`created_at` / `updated_at` / domain `recorded_at`).  
- **No** `sync_status` column on web (that is Flutter local-queue metadata).  
- Triggers: `extensions.moddatetime(updated_at)` on all mutable tables (same lesson as Flutter Supabase sync).  
- RLS optional later; MVP enforces tenancy in SQL `WHERE user_id = $authUser`.

### 4.2 Tables (MVP)

**User-owned**

| Table | Notes |
|-------|--------|
| `exercise_records` | sets: type/name/keys, set#, weight, reps, rpe, `set_type`, `recorded_at` |
| `diet_records` | meal_type, food_name, macros, Atwater-derived kcal, satisfaction, memo |
| `weight_records` | weight_kg, `recorded_at` |
| `exercise_templates` | name, exercise_type, nullable `exercise_key`/`category_key`, `rest_seconds_override` |
| `food_templates` | name, macros, derived calories |
| `workout_routines` | name, `items_json JSONB` |
| `user_profiles` | 1 row per user: gender, age, height_m, goal, activity_level, training_intensity, target_calories (+ optional macro targets) |

**Local-only in Flutter — web decision**

| Table | Web MVP |
|-------|---------|
| `daily_routine` | **Include** (date + `items_json`) — useful for planner; keyed by `(user_id, date)` |

**Admin / shared read models** (seed or sync from Flutter master export)

| Table | Access |
|-------|--------|
| `exercise_categories` | read-all authenticated |
| `exercise_category_translations` | read-all |
| `exercise_definitions` | read-all |
| `exercise_translations` | read-all |
| `one_rm_formulas` | read-all |

Seed strategy: import `assets/master_exercise_seed_v12_6.json` (or SQL dump) into migrate job. No 24h TTL client cache required if API serves from Postgres.

### 4.3 Example DDL sketch (`exercise_records`)

```sql
CREATE TABLE exercise_records (
  uuid UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  recorded_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  is_deleted BOOLEAN NOT NULL DEFAULT false,
  exercise_type TEXT NOT NULL,
  exercise_name TEXT NOT NULL,
  exercise_key TEXT,
  category_key TEXT,
  current_set INT NOT NULL,
  weight DOUBLE PRECISION NOT NULL,
  reps INT NOT NULL,
  rpe DOUBLE PRECISION NOT NULL,
  set_type TEXT NOT NULL DEFAULT 'work'
    CHECK (set_type IN ('warmup','work','drop','failure'))
);
CREATE INDEX idx_exercise_records_user_time
  ON exercise_records (user_id, recorded_at DESC)
  WHERE is_deleted = false;
```

(Analogous DDL for other tables in implementation PR; keep names aligned with Flutter for future bridge.)

---

## 5. HTTP API (MVP)

Base: `/api/v1`  
Auth: session cookie validated per §3  
Content-Type: `application/json`  
Errors: `{ "error": "code", "message": "..." }`

### 5.1 Identity & profile

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/me` | Current user |
| GET/PUT | `/api/v1/profile` | `user_profiles` + nutrition targets |
| POST | `/api/v1/profile/recalculate-targets` | Server-side Mifflin/TDEE/macros (port from Flutter `nutrition_calculator.dart`) |

### 5.2 Records (pattern)

For `exercise` / `diet` / `weight`:

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/{resource}?date=YYYY-MM-DD` | List for local date |
| GET | `/api/v1/{resource}?from=&to=` | Range (charts) |
| POST | `/api/v1/{resource}` | Create (client may send `uuid`) |
| PATCH | `/api/v1/{resource}/{uuid}` | Update |
| DELETE | `/api/v1/{resource}/{uuid}` | Soft delete |

### 5.3 Templates & routines

| Method | Path | Purpose |
|--------|------|---------|
| GET/POST | `/api/v1/exercise-templates` | List / create |
| PATCH/DELETE | `/api/v1/exercise-templates/{uuid}` | Update override / soft delete |
| GET/POST | `/api/v1/food-templates` | List / create |
| DELETE | `/api/v1/food-templates/{uuid}` | Soft delete |
| GET/POST | `/api/v1/workout-routines` | CRUD-ish |
| PATCH/DELETE | `/api/v1/workout-routines/{uuid}` | |
| GET/PUT | `/api/v1/daily-routine/{date}` | Upsert planner day |

### 5.4 Master data & summary

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/master/exercises?locale=ko` | Definitions + translations |
| GET | `/api/v1/summary?date=YYYY-MM-DD` | Aggregated daily summary DTO |
| GET | `/api/v1/one-rm/{exercise_key}` | Formula + optional history (P3) |

### 5.5 Out of MVP

- Gemini insight/chat (optional P3 via server-side key)  
- Food vision upload  
- Activity / HealthKit proxies  

---

## 6. Mobile web UI

### 6.1 UX constraints

- Viewport-first; content max-width **600px** centered (matches Flutter bottom-sheet rule).  
- Five tabs: Workout | Diet | Weight | Summary | Settings.  
- Fast entry over dense forms (steppers, recent foods, template picker).  
- No native plugins: hide Activity card; rest timer = in-page only.

### 6.2 Auth UX

1. App shell loads.  
2. `GET /api/me` → if 401, `location = auth login?rd=current`.  
3. After return, cookie present → load tabs.  
4. Logout: POST to Auth logout with CSRF (shared cookie doc), then clear local UI state.

### 6.3 Implementation choice (open at scaffold)

| Option | Pros | Cons |
|--------|------|------|
| Go + HTMX + templates | Same as ProcessManager dashboard culture | Less “app-like” transitions |
| Static SPA (e.g. lightweight React/Preact/Svelte) | Richer client state | Extra build pipeline |

Spec stays API-first so either can consume §5.

---

## 7. Domain logic to port (not re-invent)

Copy behavior from Flutter (pure functions → Go packages):

- `nutrition_calculator.dart` → BMR / TDEE / macros  
- `atwater_calculator.dart` → kcal from macros  
- `exercise_calorie_estimator.dart` → volume × body-part coefficients (summary only)  
- `set_type` volume rules (`work`+`drop` count)  
- 1RM formulas from `one_rm_formulas` (Mayhew/Wathen/Brzycki/Epley fallback)

Keep unit tests with golden values from Flutter tests where they exist.

---

## 8. Optional sync bridge (P4 — not MVP)

Goal: eventually let native Flutter and web share one logical dataset **without** forcing web onto Supabase.

Candidate designs (decide later):

1. **Export/import** — user-triggered JSON dump from Supabase → health API bulk upsert (simplest).  
2. **Bridge worker** — service account reads Supabase (or Flutter pushes) and writes Postgres keyed by mapped `user_id` (email match to Auth user).  
3. **Flutter dual-backend** — native gains optional ProcessManager API client (large change; avoid until web proven).

Bridge requirements if built:

- Idempotent upsert on `uuid`  
- LWW on `updated_at`  
- Soft-delete propagation  
- Explicit user linking: Auth email ↔ Supabase user  

---

## 9. Phased delivery

| Phase | Deliverable | Exit criteria |
|-------|-------------|---------------|
| P0 | Infra + auth | `GET /healthz` → 200; unauthenticated `GET /api/me` → `401 {"error":"unauthorized"}`; shared `nacfson_session` cookie validated against Auth and mapped to `AuthUser` |
| P1 | Schema + profile | Migrations + master exercise seed applied; profile `GET`/`PUT` and `POST /api/v1/profile/recalculate-targets` persist nutrition targets |
| P2 | Records, templates, routines | `exercise` / `diet` / `weight` CRUD with soft delete; exercise + food templates; workout routines; daily routine upsert. Atwater kcal computed server-side on diet writes |
| P3 | Summary + master data | `GET /api/v1/summary?date=` DTO (consumed kcal, targets, macro split, workout day, muscles, work+drop volume, avg RPE, estimated exercise kcal) and `GET /api/v1/master/exercises?locale=ko` — **MVP complete** |
| P4 | Sync bridge | Later, out of MVP (§8): native Flutter stays on SQLite + Supabase until the bridge ships |

[Showing lines 1-300 of 336. Use :301 to continue]