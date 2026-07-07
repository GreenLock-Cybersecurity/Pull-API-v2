# CLAUDE.md — Pull Events / Aurora Hall demo

This is the entry point for **Claude Code** on a fresh machine. Follow the
order below step by step. If a step fails, STOP and tell the user before
guessing.

---

## Index

- [What you are working on](#what-you-are-working-on)
- [Day-1 setup on a new Windows](#day-1-setup-on-a-new-windows)
- [Getting secrets onto the new machine](#getting-secrets-onto-the-new-machine)
- [Verify everything works](#verify-everything-works)
- [Related docs — read these before coding](#related-docs)
- [Project inventory](#project-inventory)
- [Deploy flows](#deploy-flows)
- [Debug playbook](#debug-playbook)
- [Anti-patterns](#anti-patterns)
- [Contact & ownership](#contact--ownership)

---

## What you are working on

**Aurora Hall** is a live public demo of the Pull Events multi-tenant
event ticketing platform. Three moving parts:

- **Backend** (`Pull-API-v2`) — Go/Gin on Fly.io.
- **Customer WebApp** (`PullWebApp-GL`) — React/Vite on Cloudflare Pages.
- **Staff mobile app** (`PullMobileApp-GL`) — Expo/React Native, run via
  Expo Go for now, EAS build pending.

All traffic funnels through:

```
https://aurora-hall.pages.dev/api/v1    (Cloudflare Pages Function proxy)
        ↓
https://pull-api-v2-demo.fly.dev/api/v1 (Fly.io Go backend)
```

Fictional venue "Aurora Hall" is fixed; no venue picker. Payments are
mocked via `services/mock_processor.go` (Stripe simulator).

---

## Day-1 setup on a new Windows

### 1. Install tools

Run in PowerShell as admin:

```powershell
# Package manager (usually already there on Win11)
winget --version || (Write-Host "Install winget from Microsoft Store first")

# Core tooling
winget install Git.Git -e
winget install OpenJS.NodeJS.LTS -e            # Node 20 LTS
winget install GoLang.Go.1.21 -e               # if 1.21 unavailable, latest OK
winget install Python.Python.3.12 -e
winget install GitHub.cli -e                    # gh
winget install Fly.Flyctl -e                    # flyctl (may need choco fallback)

# If Fly winget package isn't found, use this instead:
#   iwr https://fly.io/install.ps1 -useb | iex

# Restart the shell so new binaries are on PATH
```

Verify each in a fresh shell:

```powershell
git --version           # 2.40+
gh --version            # 2.x+
node --version          # v20.x
npm --version
go version              # 1.21+
flyctl version
python --version        # 3.10+
```

Node CLIs used by the mobile / web deploys:

```powershell
npm install -g eas-cli
npm install -g wrangler   # only if you'll deploy WebApp yourself
npm install -g expo-cli   # optional; `npx expo` also works
```

### 2. Sign in to each service

```powershell
# GitHub — opens browser
gh auth login
# Choose GitHub.com → HTTPS → Yes → Login with a web browser → copy code

# Fly.io — opens browser
flyctl auth login
# Sign in with diego.rodriguez@greenlock.tech (or team account)

# Cloudflare (only if deploying WebApp yourself) — opens browser
wrangler login

# Expo (only if building mobile) — opens browser
eas login
```

### 3. Clone the repos

Layout mirrors the previous machine. All under one parent folder:

```powershell
# Pick or create a workspace dir
mkdir C:\dev\Pull ; cd C:\dev\Pull

# ACTIVE repos (this is what you'll work on)
git clone https://github.com/GreenLock-Cybersecurity/Pull-API-v2.git
git clone -b dev https://github.com/GreenLock-Cybersecurity/PullWebApp-GL.git
git clone https://github.com/GreenLock-Cybersecurity/PullMobileApp-GL.git

# OPTIONAL (marketing site, untouched)
git clone https://github.com/GreenLock-Cybersecurity/Pull-Landing.git

# LEGACY — clone only if you plan to touch the HTML email templates.
# DO NOT edit anything else in here.
git clone https://github.com/GreenLock-Cybersecurity/Pull-API-Go.git
```

`PullClientDashboard` is **not on GitHub** and is not part of the demo.
Ignore it unless the user explicitly asks about it.

### 4. Install dependencies

```powershell
cd C:\dev\Pull\Pull-API-v2
go mod download

cd ..\PullWebApp-GL
npm install

cd ..\PullMobileApp-GL
npm install
```

---

## Getting secrets onto the new machine

**Real secrets are NOT in git.** You need to bring them in one of three
ways. Read this section carefully — this is where most first-day time is
lost.

### What secrets exist

Every value stored as a Fly.io secret on `pull-api-v2-demo`:

| Name | Where to fetch from | Notes |
|---|---|---|
| `JWT_SECRET` | Fly SSH or previous `.env` | Any 32+ char string. Rotate = sign out every user. |
| `APP_KEY` | Fly SSH or previous `.env` | 64 hex chars. Rotate = re-encrypt every venue. |
| `CENTRAL_SUPABASE_URL` | Supabase dashboard → Project `dqqvtehpidihahzabcxg` → Settings → API | Just a URL, not secret really. |
| `CENTRAL_SERVICE_KEY` | Same → `service_role` | KEEP SECRET. |
| `CENTRAL_ANON_KEY` | Same → `anon` | Public-safe. |
| `DEFAULT_SUPABASE_URL` | Supabase → project `oqqhffxwiizukkevzkvz` → Settings → API | Aurora Hall venue DB. |
| `DEFAULT_SERVICE_KEY` | Same → `service_role` | KEEP SECRET. |
| `DEFAULT_ANON_KEY` | Same → `anon` | Public-safe. |
| `BREVO_API_KEY` | https://app.brevo.com/settings/keys/api | `xkeysib-...`. Regenerate if you can't find the old one. |
| `BREVO_FROM_EMAIL` | Constant | `Pull Events <noreply@tickets.pullevents.com>` |
| `RESEND_API_KEY` | https://resend.com/api-keys | Optional (Brevo is primary). |
| `STRIPE_*` | Empty in the demo (DEMO_MODE=true uses mock). | Skip. |

### Option A — copy `.env` from the old machine (fastest)

If the user still has the old Windows with `D:/Backup/Proyecto-Pull/Pull-API-v2/.env`:

1. Transfer that file via **encrypted channel** (Signal, Bitwarden Send,
   1Password shared item, GPG-encrypted email). **Do NOT** send it over
   unencrypted email or Discord.
2. On the new machine, save it at `C:\dev\Pull\Pull-API-v2\.env`.

### Option B — pull from the Fly.io VM (recovers everything except APP_KEY / JWT_SECRET which are opaque digests)

```powershell
# List secrets (names + digests, no values):
flyctl secrets list --app pull-api-v2-demo

# Get actual values by SSHing into the running machine:
flyctl ssh console --app pull-api-v2-demo -C "printenv"
# Copy the output. Filter to the vars you need.
```

This works because the Fly VM has the secrets injected as env vars at runtime.

### Option C — regenerate from source dashboards

For each service listed in the "What secrets exist" table, log into the
dashboard and copy the key. This is fine for Supabase / Brevo / Resend
keys (they're not derived). But `JWT_SECRET` and `APP_KEY` are opaque
random values — if you regenerate them, **every JWT becomes invalid** and
**every stored encrypted venue credential becomes unreadable**. Only do
this on a brand-new deployment.

### Fill the frontend .env files

```
# PullWebApp-GL/.env       (development, points at demo — same as .env.production)
VITE_API_URL=/api/v1
VITE_DEFAULT_VENUE_SLUG=aurora-hall

# PullMobileApp-GL/.env    (development, points at demo)
EXPO_PUBLIC_API_URL=https://aurora-hall.pages.dev/api/v1
```

Nothing sensitive in the frontend `.env`s — the API is public. You can
also override `VITE_API_URL` to `http://localhost:8080/api/v1` when
running the backend locally.

---

## Verify everything works

Run this smoke test in Bash / Git Bash after setup:

```bash
# 1. Backend reachable
curl -sf https://pull-api-v2-demo.fly.dev/health || echo "BACKEND DOWN"

# 2. Login works (returns token)
TOKEN=$(curl -s -X POST "https://aurora-hall.pages.dev/api/v1/auth/login-staff" \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@aurorahall.com","password":"DemoStaff2026!"}' \
  | python -c "import sys,json; print(json.load(sys.stdin).get('token',''))")
echo "TOKEN: ${TOKEN:0:30}..."

# 3. JWT decodes correctly (should include employee_id, organization_id, email, name)
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | python -m json.tool

# 4. Eventos array reachable
curl -sf "https://aurora-hall.pages.dev/api/v1/venues/aurora-hall/events" \
  | python -c "import sys,json; d=json.load(sys.stdin); print(f'{d.get(\"count\")} events')"

# 5. Local backend build passes
cd C:/dev/Pull/Pull-API-v2 && go build ./...
echo $?    # 0 = green

# 6. WebApp installs and builds
cd C:/dev/Pull/PullWebApp-GL && npm run build
# should end with `built in Xs`

# 7. Mobile expo-doctor
cd C:/dev/Pull/PullMobileApp-GL && npx expo-doctor
# should be 18/18 or similar
```

All green? You're set. Any red? Fix before doing anything else — a broken
setup wastes hours later.

---

## Related docs

Read these three IN ORDER at the start of any session:

1. **[HANDOFF.md](HANDOFF.md)** — every bug I hunted so far with root
   cause. Prevents you from re-fixing the same shape mismatches.
2. **[TODO.md](TODO.md)** — prioritized backlog. P0 first, then P1.
   Delete items as you finish them.
3. **[ARCHITECTURE.md](ARCHITECTURE.md)** — system diagram, multi-tenant
   model, JWT claims, compat layer explanation.

If those docs disagree with the code, **TRUST THE CODE** and update the
docs after you resolve the confusion.

---

## Project inventory

| Project | Type | Stack | Location | State |
|---|---|---|---|---|
| **Pull-API-v2** | Backend (ACTIVE) | Go 1.21, Gin | `/Pull-API-v2` | Deployed on Fly.io as `pull-api-v2-demo` |
| **PullWebApp-GL** | Customer web (ACTIVE) | React 19, Vite, TS | `/PullWebApp-GL` | Cloudflare Pages at `aurora-hall.pages.dev`. Branch: `dev`. |
| **PullMobileApp-GL** | Staff mobile (ACTIVE) | Expo 54, RN 0.81 | `/PullMobileApp-GL` | Runs in Expo Go; EAS build pending |
| Pull-API-Go | **LEGACY** — do not use | Go 1.24, Gin | `/Pull-API-Go` | Kept only for HTML email templates (v2 embeds them via `//go:embed`) |
| PullClientDashboard | **Not used in demo** | React 19 | `/PullClientDashboard` | Points at `api.pullevents.com` (dead). Ignore. |
| Pull-Landing | Marketing (untouched) | React 19, Tailwind | `/Pull-Landing` | Clean repo |

### Demo credentials

**Staff (mobile / any staff endpoint)**:
```
Email:    demo@aurorahall.com
Password: DemoStaff2026!
Role:     admin
Venue:    Aurora Hall
```

**Customer WebApp**: no auth needed, anonymous checkout.

**Real secrets**: `Pull-API-v2/.env` (gitignored). See
"Getting secrets" above.

---

## Deploy flows

### Backend (Fly.io)

```bash
cd Pull-API-v2
go build ./...                                       # local sanity check
flyctl deploy --remote-only --strategy immediate     # ~60–90s
flyctl status --app pull-api-v2-demo                 # confirm STARTED + passing
```

If the machine ends STOPPED after deploy:

```bash
flyctl machine start <machine-id> --app pull-api-v2-demo
```

Common cause: build succeeded but the app panicked on boot (see logs).
The `UseVIPListFlow` typo did exactly that — local `go build` was cached
but Fly's remote builder failed silently, keeping the previous machine
until a health-check probe killed it. **Always** verify the response
shape after deploying a change — don't trust "deploy succeeded".

### WebApp (Cloudflare Pages)

Auto-deploys on push to `dev` branch. Manual:

```bash
cd PullWebApp-GL
npm run build
wrangler pages deploy dist --project-name aurora-hall
```

The Pages Function proxy lives at
`PullWebApp-GL/functions/api/[[path]].js` and ships with every build.

### Mobile app (EAS → TestFlight)

Full walkthrough in `PullMobileApp-GL/BUILD_INSTRUCTIONS.md`. Summary:

```bash
cd PullMobileApp-GL
eas login
eas build --platform ios --profile production
eas submit --platform ios --profile production --latest
```

Bundle id: `com.pullevents.staff`. EAS project id:
`cc92c30d-3724-45c7-913f-6774f3a1ebfb`.

Push notifications require the EAS build — they DO NOT work in Expo Go.

---

## Debug playbook

### Read live logs

```bash
# Last 60 lines, all traffic:
flyctl logs --app pull-api-v2-demo | tail -60

# Only errors (4xx/5xx), excluding 401 (expected on auth flows):
flyctl logs --app pull-api-v2-demo | tail -200 \
  | grep -E "GIN.*(4[0-9]{2}|5[0-9]{2})" | grep -v 401

# By request-id (found in error body):
flyctl logs --app pull-api-v2-demo | grep "<request-id>"
```

### Verify a wire shape from CLI

```bash
API="https://aurora-hall.pages.dev/api/v1"
TOKEN=$(curl -s -X POST "$API/auth/login-staff" \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@aurorahall.com","password":"DemoStaff2026!"}' \
  | python -c "import sys,json; print(json.load(sys.stdin).get('token',''))")

# Now hit any endpoint:
curl -s -H "Authorization: Bearer $TOKEN" "$API/employees/employees" \
  | python -m json.tool | head
```

### Inspect a JWT

```bash
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | python -m json.tool
```

Should print:

```json
{
  "iss": "pull-api-v2",
  "exp": 1782359437,
  "iat": 1782273037,
  "user_id":         "5032b851-...",
  "employee_id":     "5032b851-...",  <-- alias, same as user_id
  "email":           "demo@aurorahall.com",
  "name":            "Demo Admin",
  "venue_id":        "8450e956-...",
  "organization_id": "74f2fa79-...",
  "role":            "admin",
  "type":            "venue_staff"
}
```

If a field is missing, the login is generating a partial token — check
`controllers/auth_controller.go:LoginStaff`.

### Common Postgres error codes from Supabase

- **`42703`** = column doesn't exist. Either narrow your `select` or fix
  the schema query.
- **`22P02`** = invalid input for enum. `reservation_status`:
  `pending|confirmed|closed|completed|cancelled`. `order_status`:
  `pending|processing|confirmed|failed|cancelled`.
- **`23505`** = unique constraint violation.
- **`23503`** = foreign key violation.

---

## Anti-patterns

Learned the hard way in the previous session:

- **Don't touch `Pull-API-Go`** for anything other than HTML email
  templates. It's not deployed anywhere; changes go nowhere and confuse
  the next dev.
- **Don't touch `PullClientDashboard`** for the Aurora demo. Different
  scope.
- **Don't `git add -A`** in `Pull-API-v2` without checking `.env` isn't
  staged. `.gitignore` covers it but be paranoid.
- **Don't DELETE demo data as a smoke test.** I once soft-deleted
  Aurora Friday Nights with a raw `DELETE /event/delete-event/:id` and
  had to `PUT status=published, deleted_at=null` to bring it back. Test
  destructive endpoints against a throwaway event.
- **Don't trust `deploy succeeded`.** Fly's remote builder can fail
  silently in ways local `go build` misses (see the `UseVIPListFlow` /
  `UseVipListFlow` incident). Always verify the response shape of the
  endpoint you touched.
- **Don't add routes to `setupMobileRoutes`** without checking whether
  the same path is already registered. Duplicating `GET
  /guest-lists/venue/:venueId/pending` panicked boot and left the
  machine stopped.
- **Don't do `select "*"` blindly** on tables with new enum columns.
  `EnrichEvent` currently trips on some rows. Prefer narrow selects and
  add columns as you need them.

---

## Contact & ownership

- GitHub org: `GreenLock-Cybersecurity`
- Primary dev: `diego.rodriguez@greenlock.tech`
- Fly.io account: same email
- Supabase projects owned by the same org
- Brevo / Resend / Cloudflare tied to the same team account

If you're unsure whether a change is "authorized to ship", ask the user.
The demo is public and clients see it — a broken demo is worse than a
delayed feature.
