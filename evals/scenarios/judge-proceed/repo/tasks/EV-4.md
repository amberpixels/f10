# EV-4: Add a health-check endpoint

OPEN · created 2026-11-24

The load balancer needs an endpoint to probe so it can take a dead instance out of rotation. Add one that answers without touching the notes directory.

## Done when

- The load balancer can probe the server.
