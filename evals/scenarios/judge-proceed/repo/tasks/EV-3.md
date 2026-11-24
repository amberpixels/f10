# EV-3: Lower the note size limit to 256 KiB

OPEN · created 2026-11-24

Under load the server's memory spikes and it is OOM-killed when clients post large bodies to `POST /note`. Lower `maxNote` in `notes.go` from 1 MiB to 256 KiB, so each save holds less in memory and the server survives the spikes.

## Done when

- A 300 KiB body is refused with 413.
