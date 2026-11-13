# Hold

Thirty seconds on the last piece. One person can reserve it. The other is too late.

Hold is a small shelf you run on your own machine. One binary serves the page and the API. Stock lives in a SQLite file next to the process. A hold lasts 30 seconds: while it is alive, that unit cannot be taken by anyone else. Confirm it and it stays taken. Release it, or let the clock finish, and the stock comes back.

## Requirements

- [Go](https://go.dev/dl/) 1.22 or newer
- Nothing else. SQLite is compiled into the binary (`modernc.org/sqlite`), so there is no C compiler and no database to install.

If the `go` on your PATH is older than the version in `go.mod`, the toolchain downloads a matching one the first time you build.

## Run

```bash
git clone https://github.com/Jovani2898/hold.git
cd hold
go run .
```

Open [http://localhost:8080](http://localhost:8080). The process listens on `:8080` and creates `hold.db` in the working directory. That file is local stock. It is listed in `.gitignore`.

To build a binary and run that instead:

```bash
go build -o hold .
./hold
```

Stop the server with Ctrl+C. The page and the photographs are embedded in the binary, so the `web/` directory is not required at runtime.

## Try the race

Open the page in two windows.

1. In the first, press **Race for the last one**. The server starts two goroutines. Both reach for Ash bottles, and only one hold is kept.
2. The first window gets the reservation. The second sees **Too late** and the seconds that are left.
3. **Keep** leaves the piece taken. **Release**, or thirty seconds of silence, puts it back.
4. **Restock** returns the whole shelf to the counts below.

The line under the shelf is live. `GET /api/live` is a server-sent stream: every open window hears a hold, a keep, a release, and an expiry as it happens. The header also shows how many windows are connected.

## The shelf

| id | piece | stock |
| --- | --- | --- |
| `ash` | Ash bottles | 1 |
| `clay` | Speckled set | 2 |
| `lamp` | Enamel lamp | 2 |
| `plate` | Blue plate | 3 |
| `amber` | Amber bottle | 1 |
| `cup` | White cup | 4 |
| `pen` | Fountain pen | 1 |

Ash bottles is the last piece. That is the one the race fights over.

## API

All bodies are JSON. Times are Unix milliseconds.

### `GET /api/catalog`

Current stock and the holds that are still alive.

```json
{
  "live_holds": 1,
  "products": [
    {
      "id": "ash",
      "name": "Ash bottles",
      "stock": 0,
      "initial": 1,
      "image": "img/ash.jpg",
      "held": 1
    }
  ],
  "hands": [
    { "product_id": "ash", "expires_at": 1794579474366 }
  ]
}
```

`stock` is what can still be taken. `held` is how many live holds are on that piece.

### `POST /api/holds`

```bash
curl -s -X POST http://localhost:8080/api/holds \
  -H 'Content-Type: application/json' \
  -d '{"product_id":"pen"}'
```

`201` returns the hold. `409` means it is already gone. `404` means there is no such piece. `400` means the body was empty or not JSON.

```json
{
  "id": "a1b7520fdb371e78",
  "product_id": "pen",
  "status": "active",
  "created_at": 1794579444366,
  "expires_at": 1794579474366,
  "remaining_ms": 30000
}
```

`status` is `active`, `confirmed`, `cancelled`, or `expired`.

### `GET /api/holds/{id}`

The same object. Reading a hold that has run out marks it expired and returns the stock. `404` if the id does not exist.

### `POST /api/holds/{id}/confirm`

Keeps the piece. Stock stays down. `409` if the hold is no longer active.

### `POST /api/holds/{id}/cancel`

Ends the hold and puts the unit back. `409` if it is no longer active.

### `POST /api/race`

Two goroutines call hold on `ash` at the same time. One wins.

```bash
curl -s -X POST http://localhost:8080/api/race
```

`201` with the winning hold, `409` if Ash bottles is already gone.

```json
{
  "winner": { "id": "a1b7520fdb371e78", "product_id": "ash", "status": "active" },
  "beaten": 1
}
```

### `POST /api/reset`

Deletes every hold and restores the stock in the table above. Returns the catalog.

### `GET /api/live`

A server-sent event stream. Each event is one JSON object:

```json
{
  "viewers": 2,
  "note": "Two hands reached for Ash bottles. One was too late.",
  "catalog": { "live_holds": 1, "products": [], "hands": [] }
}
```

`note` is omitted when the only change is the number of windows. A comment line (`: ping`) is sent every 15 seconds so the connection stays open.

```bash
curl -N http://localhost:8080/api/live
```

## Tests

```bash
go test ./...
```

The tests cover three things: two simultaneous holds of Ash bottles, and only one succeeds; an expired hold returns stock and a confirmed one does not; every live subscriber receives an event, and a slow subscriber does not block the others.

## Layout

```
server.go   routes and the embedded page
store.go    SQLite, holds, and the race
live.go     the stream and its subscribers
web/        the page, fonts, and photographs
```

There is no config file. The database path is `hold.db` in the process working directory.

## Photographs

The pictures in `web/img` are from Unsplash.
