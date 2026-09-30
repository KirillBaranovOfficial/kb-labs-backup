---
id: S-020
title: Studio — First login
persona: solo-developer
area: studio
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

Open Studio on a fresh host, reach the dashboard in local mode or after logging in on a secured host, and return to Studio with the session intact.

## Prerequisites

- [ ] Platform installed and started with `kb-dev`.
- [ ] Studio base URL recorded by the installation; secured mode has a valid user account.
- [ ] A browser is available.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-020.1 | Open the Studio base URL in local mode | Studio opens without a login form and displays the Dashboard page. |
| S-020.2 | Open the Studio base URL in secured mode | Studio displays the login form with email and password fields. |
| S-020.3 | In secured mode, submit the valid account credentials | The login form closes and the Dashboard page is displayed. |
| S-020.4 | In secured mode, submit the same email with an incorrect password | The login page remains visible and displays `Invalid credentials`; Studio does not render a blank page. |
| S-020.5 | Refresh Studio after entering the dashboard in secured mode | The authenticated dashboard remains visible while the session is valid. |
| S-020.6 | Navigate from the dashboard using the Studio sidebar to Projects, Analytics, and Observability | Each selected page title is visible and the sidebar marks the selected section. |
