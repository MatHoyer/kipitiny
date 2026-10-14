---
title: Appearance
description: Give the manager your own name, accent colour and logo
order: 19
---

*System › Appearance* (admins) brands the manager for everyone who uses it:

- **Name**: shown in the sidebar, on the sign-in page, in the browser tab and
  in emails (invites, notifications, the test email), up to 40 characters.
- **Accent colour**: buttons, focus rings and the bar on top of emails, in
  light and dark mode alike. Text on it turns black or white, whichever reads
  better. Leave it empty for kipitiny's black and white.
- **Logo**: next to the name and as the favicon. PNG, JPEG, WebP or SVG, up to
  128 KB; square works best.

The preview shows the result before you save; **Reset** goes back to
kipitiny's own look. Notification emails keep their level's colour (red for a
failure, green for a success).

The logo is public at `/api/branding/logo`, since the sign-in page shows it.
An SVG logo is only ever drawn as an image, and opening it directly runs no
script.
