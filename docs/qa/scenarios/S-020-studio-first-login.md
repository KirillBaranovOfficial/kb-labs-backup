---
id: S-020
title: Studio — First login
persona: solo-developer
area: studio
priority: P0
status: active
release: true
modes: [secured]
surfaces: [studio]
---

## Goal
Developer opens Studio in browser, logs in, and sees the dashboard.

## Prerequisites
- [ ] Platform installed, `kb-dev start` done
- [ ] Studio running on port 3000 (or 3002 behind nginx)
- [ ] Browser available

---

## Steps

### Phase 1 — Load Studio

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-020.1 | Open the Studio base URL served by the installed host, using the host URL recorded by the V2 install | Studio loads, no blank page, no JS errors in console | | ⬜ |
| S-020.2 | Login page appears | Has username + password fields | | ⬜ |
| S-020.3 | Page loads in < 3s | No noticeable lag | | ⬜ |

### Phase 2 — Login

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-020.4 | Enter valid credentials (admin@... / password) | Login succeeds, redirects to dashboard | | ⬜ |
| S-020.5 | Dashboard shows platform status (services, plugins) | Data visible, not empty | | ⬜ |
| S-020.6 | No JS errors in browser console after login | Console clean | | ⬜ |

### Phase 3 — Wrong credentials

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-020.7 | Enter wrong password | "Invalid credentials" shown — not blank/crash | | ⬜ |
| S-020.8 | After 5 wrong attempts | Rate limit message — not silent 429 | | ⬜ |

### Phase 4 — Session

| # | Action | Expected | Actual | Status |
|---|--------|----------|--------|--------|
| S-020.9 | Refresh page after login | Stays logged in | | ⬜ |
| S-020.10 | Close tab, reopen Studio | Session persists (or re-login prompted cleanly) | | ⬜ |

---

## Result
## Bugs
## Notes