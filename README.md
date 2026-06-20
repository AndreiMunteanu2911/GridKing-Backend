# GridKing Backend

The GridKing backend is the authoritative game server for multiplayer American Checkers. It verifies Firebase users, owns every legal board transition, runs the bot AI, matches online players, calculates ranked MMR, and persists results to Firestore.

Players never decide whether a move is legal. The frontend submits a move path and this service either applies it or rejects it.

## What The Server Handles

- Firebase ID-token verification
- Unique profile creation
- Firestore profile and leaderboard data
- Casual and ranked matchmaking queues
- Authenticated WebSocket connections
- Turn synchronization and validated board state
- Mandatory captures and complete multi-jump enforcement
- Red and Black king promotion
- Resignation and opponent-disconnection results
- Easy, Medium, and Hard Minimax bots with alpha-beta pruning
- Ranked Elo/MMR calculations and match history

## Requirements

- Go version compatible with `go.mod`
- A Firebase project
- Email/Password Authentication enabled
- A Firestore database
- Firebase Admin credentials

## Firebase Admin Setup

In Firebase Console:

1. Open **Project settings → Service accounts**.
2. Generate a new private key.
3. Keep the downloaded JSON outside the repository.

For local development, use Application Default Credentials:

```powershell
$env:GOOGLE_APPLICATION_CREDENTIALS='C:\secure\gridking-service-account.json'
$env:FIREBASE_PROJECT_ID='your-project-id'
```

For Render, store the complete one-line JSON value in `FIREBASE_SERVICE_ACCOUNT_JSON`. Never expose this JSON to Angular, Vercel, an APK, or GitHub repository variables.

## Environment Variables

Copy `.env.example` to `.env.local` as a reference. The Go process reads operating-system environment variables directly; it does not automatically load dotenv files.

```dotenv
PORT=8080
FRONTEND_ORIGIN=http://localhost:4200
FIREBASE_PROJECT_ID=your-firebase-project-id
FIREBASE_SERVICE_ACCOUNT_JSON={"type":"service_account","project_id":"your-project-id"}
```

`FIREBASE_SERVICE_ACCOUNT_JSON` is optional locally when `GOOGLE_APPLICATION_CREDENTIALS` points to the service-account file.

`FRONTEND_ORIGIN` must be an exact origin without a trailing slash. It protects both HTTP CORS and the WebSocket origin check.

## Run Locally

PowerShell example:

```powershell
$env:PORT='8080'
$env:FRONTEND_ORIGIN='http://localhost:4200'
$env:FIREBASE_PROJECT_ID='your-project-id'
$env:GOOGLE_APPLICATION_CREDENTIALS='C:\secure\gridking-service-account.json'
go run ./cmd/server
```

The health endpoint is:

```text
http://localhost:8080/health
```

Then configure the frontend with:

```dotenv
GRIDKING_API_URL=http://localhost:8080
GRIDKING_WS_URL=ws://localhost:8080/ws
```

## Deploy To Render

The included `render.yaml` defines a Go web service on the `master` branch.

1. Push the backend repository to GitHub.
2. In Render, create a new **Blueprint** and select the repository.
3. Confirm the Blueprint finds `render.yaml` at the repository root.
4. Set the prompted environment variables:

```text
FRONTEND_ORIGIN=https://your-gridking-frontend.vercel.app
FIREBASE_PROJECT_ID=your-firebase-project-id
FIREBASE_SERVICE_ACCOUNT_JSON={complete service-account JSON on one line}
```

5. Deploy the service.
6. Confirm `https://your-service.onrender.com/health` returns `{"status":"ok"}`.

The Blueprint uses one Starter instance because matchmaking and live matches are held in memory. Do not increase the instance count without adding shared matchmaking/session infrastructure such as Redis or a dedicated message broker. A continuously available paid instance is appropriate for production WebSockets; sleeping instances interrupt live connections.

After Render is live, use its URLs in the frontend:

```text
GRIDKING_API_URL=https://your-service.onrender.com
GRIDKING_WS_URL=wss://your-service.onrender.com/ws
```

## Deployment Order

Use this order for a first production deployment:

1. Configure Firebase Authentication and Firestore.
2. Deploy Firestore rules from the frontend repository.
3. Deploy this backend to Render with a temporary or known frontend origin.
4. Deploy the frontend to Vercel using the Render HTTPS/WSS URLs.
5. Add the Vercel hostname to Firebase Authorized Domains.
6. Set `FRONTEND_ORIGIN` in Render to the exact Vercel origin.
7. Redeploy/restart Render and perform an online match check.

## HTTP API

```text
GET  /health                 Public health check
POST /api/profiles           Create the authenticated player's profile
GET  /api/profiles/me        Read the authenticated player's profile
GET  /api/leaderboard        Read ranked profiles
POST /api/bot/start          Create/reset a server-owned bot session
POST /api/bot/move           Submit a move to the current bot session
GET  /ws                     Open an authenticated multiplayer WebSocket
```

Protected HTTP endpoints require:

```text
Authorization: Bearer <firebase-id-token>
```

## WebSocket Messages

Client messages:

```json
{"type":"join_queue","mode":"casual"}
{"type":"join_queue","mode":"ranked"}
{"type":"move","move":{"path":[42,28,14]}}
{"type":"resign"}
{"type":"leave_queue"}
```

Server event types include:

```text
queued
match_found
state
game_over
opponent_disconnected
error
```

The server sends the complete 64-square board and currently legal move paths. A multi-jump path must include every required landing square.

## Board Representation

The board is a flat array of 64 integers:

```text
0 = empty
1 = red normal piece
2 = black normal piece
3 = red king
4 = black king
```

Red moves first. Normal pieces move forward diagonally. Kings move in both diagonal directions. Captures are mandatory across the entire side, and a player must finish the selected jump chain.

## Bot Difficulty

- Easy: depth 2, material-focused evaluation
- Medium: depth 4, material and edge safety
- Hard: depth 6, material, edge safety, and promotion progress

Bot state remains on the server and is scoped to the authenticated user. The client cannot submit a replacement board.

## Ranked MMR

Ranked completion uses an Elo calculation with a K-factor of 32. Firestore updates both player documents and creates the match-history document in one transaction. Casual games update wins and matches played without changing MMR.

## Project Map

```text
cmd/server/main.go            Firebase initialization and HTTP lifecycle
internal/api/                 HTTP profile and bot handlers
internal/auth/                Firebase token middleware
internal/bot/                 Minimax and evaluation logic
internal/game/                Authoritative American Checkers rules
internal/profile/             Firestore profiles, MMR, and match history
internal/realtime/            Matchmaking and WebSocket match sessions
render.yaml                   Render Blueprint
.env.example                  Required environment variable template
```

## Production Notes

- Use HTTPS/WSS in every deployed client.
- Keep Firebase Admin credentials server-only.
- Keep the Render service at one instance until matchmaking state is externalized.
- Set an exact production `FRONTEND_ORIGIN`.
- Monitor disconnect rates and Firestore transaction failures before increasing traffic.
