---
title: Photo and voice
description: In Termivoa, send a phone photo to Claude Code or Codex without its location data, or speak a prompt that you check before you send it.
lastUpdated: 2026-10-01
---

Termivoa lets you send a photo to a coding agent, or speak a prompt, from your phone. Both are under **compose** when you have control of a session.

## Send a photo

1. Tap **Photo**, then take or pick a photo.
2. When the upload is done, the draft starts with `@.termivoa/uploads/<name>`. This tells Claude Code and Codex to look at the file.
3. Add your prompt after it and tap **Send ⏎**.

**✕** removes the photo from the message. The file stays: uploads are never deleted on their own.

## What happens to the photo?

- The phone removes location (GPS) and other photo data (EXIF) before sending.
- The long side is scaled down to 2048 pixels at most.
- Only JPEG, PNG, WebP and GIF up to 5 MB are accepted.
- It is saved in the session's project folder, in `.termivoa/uploads/`. A `.gitignore` in `.termivoa/` keeps uploads out of git.

## Speak a prompt

1. Tap **Voice** and allow the microphone if asked.
2. Speak. Your words show as you talk.
3. Tap **Done · edit** to add them to the draft, or **Cancel** to drop them.
4. Check the text, then tap **Send ⏎**.

Voice never sends on its own. Listening stops when you close the panel or lock the phone.

## Where does my voice go?

Voice uses your browser's speech service. On iPhone, Safari may send the audio to Apple. Termivoa sends no audio anywhere. Without a browser speech service, **Voice** is hidden: use your keyboard's microphone key.
