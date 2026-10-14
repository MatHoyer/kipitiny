---
title: Appearance
description: Give the manager your own name, logo and theme
order: 19
---

*System › Appearance* (admins) brands the manager for everyone who uses it.
The whole UI shows your changes while you edit, and goes back if you leave
without saving. **Reset** returns to kipitiny's own look.

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
- **Logo**: next to the name and as the favicon. PNG, JPEG, WebP or SVG, up to
  128 KB; square works best.

Notification emails keep their level's colour (red for a failure, green for a
success).

The logo is public at `/api/branding/logo`, since the sign-in page shows it.
An SVG logo is only ever drawn as an image, and opening it directly runs no
script.
