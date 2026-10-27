---
title: Appearance
description: Give the manager your own name, logo and theme
order: 19
---

*System › Appearance* (admins) brands the manager for everyone who uses it.
The whole UI shows your changes while you edit, and goes back if you leave
without saving. **Reset** returns to kipitiny's own look.

Light or dark mode is each user's own: **Preferences** (user menu)
sets it on every browser they sign in to, *System* following the device.

## Theme

Start from a preset (kipitiny, Nord, Solarized, Forest, Midnight, Rose,
Paper, Terminal), then adjust any part of it:

- **Accent**: buttons, focus rings and the bar on top of emails. Text on it
  turns black or white, whichever reads better. Empty keeps kipitiny's black
  and white.
- **Accent in dark mode**: replaces the accent in dark mode; a lighter shade
  usually reads better there. Empty uses the accent.
- **Tint**: a colour mixed lightly into backgrounds, surfaces and borders.
  Empty keeps them neutral grey.
- **Corners**: square, small, medium (default), large or round.
- **Font**: Geist (default), your system's font, serif or mono.
- **Density**: comfortable (default) or compact, which tightens every
  padding and gap.

## Name and logo

- **Name**: shown in the sidebar, on the sign-in page, in the browser tab and
  in emails (invites, notifications, the test email), up to 40 characters.
- **Logo**: next to the name, as the favicon and as the app's icon. PNG, JPEG,
  WebP or SVG, up to 128 KB; square works best.

Notification emails keep their level's colour (red for a failure, green for a
success).

## Install as an app

The manager can be installed like an app, in its own window without the
browser's bars: **Install** in the address bar on Chrome and Edge, *Share ›
Add to Home Screen* on iPhone and iPad, *Add to Home screen* on Android. The
app takes the name, accent and logo set here. Use a PNG logo, at least 512 px
wide, to get a sharp icon everywhere: iOS shows no SVG icon.

Browsers offer this only over HTTPS (or on `localhost`).

The logo is public at `/api/branding/logo`, since the sign-in page shows it.
An SVG logo is only ever drawn as an image, and opening it directly runs no
script.
