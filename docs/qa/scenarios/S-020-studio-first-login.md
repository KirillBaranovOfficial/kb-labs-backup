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
A team administrator opens secured Studio, signs in with the first-admin account, and reaches the dashboard.

## Prerequisites
- [ ] Secured platform and Studio are running; use the installed host URL rather than assuming a port.
- [ ] First-admin email and password are available.

---

## Steps

| # | Action | Expected |
|---|---|---|
| S-020.1 | Open the secured Studio base URL | Browser displays the login page with email and password inputs. |
| S-020.2 | Submit the configured admin email and password | Studio calls `POST /api/auth/login`, then loads `/api/auth/me`; the browser lands on the dashboard and displays the authenticated account. |
| S-020.3 | Reload the dashboard in the same browser profile | The session is restored from the secure session cookies and the dashboard remains available. |
| S-020.4 | Click `Logout` in the Studio sidebar, then submit the correct email with an incorrect password | Login stays on the login page and displays “Invalid credentials”; no authenticated dashboard is shown. |
