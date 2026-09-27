---
title: Photo and voice
description: Send a photo to Claude Code or Codex from your phone, and dictate a prompt with your voice.
---

This page helps you send a photo to a coding agent and speak a prompt.

Both are in the composer. Open a session, take control, and tap **compose**.

## Send a photo

1. Tap **Photo**. You need control.
2. Take a photo or pick one from your library.
3. Wait until the row shows that the photo is on your computer.
4. The draft now starts with `@.termivoa/uploads/<name>`. Claude Code and Codex read this as "look at this file".
5. Type your prompt after it, for example "describe this image".
6. Tap **Send ⏎**.

### What happens to the photo

- The phone removes location data (GPS) and other photo data (EXIF) before it sends the photo.
- Large photos are made smaller: the long side is at most 2048 pixels.
- Your computer saves it in the session's project folder, in `.termivoa/uploads/`.
- Termivoa adds a `.gitignore` file there, so git never commits your uploads.
- Only JPEG, PNG, WebP and GIF files up to 5 MB are accepted.

Tap **✕** to remove the photo from the message. The file stays on your computer. Uploads are never deleted on their own.

## Speak a prompt

1. Tap **Voice**.
2. Allow the microphone if the phone asks.
3. Speak. Your words show as you talk.
4. Tap **Done · edit** to add the words to the draft. Tap **Cancel** to drop them.
5. Check the text, then tap **Send ⏎**.

Voice never sends on its own. Listening stops when you close the panel or lock the phone.

Voice uses your browser's speech service. On iPhone, Safari may send the audio to Apple's speech service. Termivoa itself sends no audio anywhere.

If your browser has no speech service, **Voice** is hidden. Use the microphone key on your phone keyboard in its place.
