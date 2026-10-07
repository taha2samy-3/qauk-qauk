# WebSocket contract suite

This is a black-box test suite for the realtime WebSocket protocol, written in Go. It pins down how the current
Django/Channels backend behaves. Later the same tests run, unchanged, against the Go gateway (plan §7, Phase 0 and
Phase 4).

The tests never import server code. They reach a backend in three ways only:

1. WebSockets (`/device/node_red/` and `/browser/simple/`).
2. A **fixture** JSON file that describes the seeded data.
3. A **hook** executable that changes server state (grants, elements, devices, keys).

Every Go file starts with `//go:build contract`, so `go build ./...` and `go test ./...` skip the suite.

```sh
# Full run against the Go backend (needs `task infra:up`)
task contract:go                     # or: bash server/contracttest/go/run.sh

# Against any backend that is already running
cd server && CONTRACT_FIXTURE=... CONTRACT_HOOK=... CONTRACT_KNOWN_BUGS=... \
  go test -tags contract ./contracttest/... -v
```

## Interface (backend contract)

### Environment

| Var | Meaning |
|---|---|
| `CONTRACT_FIXTURE` | Path to the fixture JSON (schema below). Required. |
| `CONTRACT_HOOK` | An executable, invoked as `$CONTRACT_HOOK <action> <args...>`. It prints optional output (for example a new id) on stdout and exits 0 on success. Required. |
| `CONTRACT_KNOWN_BUGS` | Comma list of bug ids that the backend still has, for example `B1,B2,B3`. |

### Fixture schema

```jsonc
{
  "ws_base": "ws://127.0.0.1:8765",            // scheme://host:port, no trailing slash
  "origin":  "http://127.0.0.1:8765",          // an allowed Origin, sent on every browser socket
  "paths":   { "device": "/device/node_red/", "browser": "/browser/simple/" },  // optional, these are the defaults

  // map: role -> device
  "devices": {
    "<role>": {
      "id": "uuid",                            // JWT claim `id`
      "name": "string",
      "key_id": "uuid | \"\"",                 // id of the stored public key ("" when none is assigned)
      "private_key_pem": "-----BEGIN PRIVATE KEY-----...",  // PKCS#8 (PKCS#1 / SEC1 also accepted)
      "alg": "RS256 | ES256"
    }
  },
  // map: role -> element
  "elements": {
    "<role>": { "id": "uuid", "name": "string", "device_id": "uuid", "points": 5, "details": {} /* or null */ }
  },
  // map: role -> user
  "users": {
    "<role>": { "id": "string", "username": "string", "cookie": "sessionid=..." /* full Cookie header value */ }
  },
  // map: role -> group name
  "groups": { "readers": "contract-readers" },
  "grants": { ... }                            // informational only, not read by the tests
}
```

Required roles and the grants the tests rely on:

| Kind | Role | Requirements |
|---|---|---|
| device | `main` | Active key. Owns `sensor`, `switch`, `history`. |
| device | `second` | Active key. Owns `foreign`. The suite uses ES256 for it so both algorithms are covered. |
| device | `nokey` | Exists, but no key is assigned. `private_key_pem` is a key that the server does not know. |
| device | `inactive` | Its key is stored with `is_active = false`. |
| device | `disposable` | Active key. Owns `disposable`. The tests delete this device. |
| device | `rotate` | Active key. Owns `rotate` (Django only force-closes devices that have at least one element). |
| device | `status` | Active key. Owns `status`. No other test connects it. |
| element | `sensor` | On `main`. `points` 10, `details` non-null. |
| element | `switch` | On `main`. |
| element | `history` | On `main`. `points = 5`. |
| element | `foreign` | On `second`. |
| element | `disposable`, `rotate`, `status` | On the devices of the same name. |
| user | `rc` | `RC` on `sensor`, `switch`, `history`, `status`, `disposable`, `rotate`. `R` on `foreign`. |
| user | `r` | `R` on `sensor` and `switch`. |
| user | `noperm` | No grants. Not in any group. |
| user | `group_r` | No direct grants. Member of `readers`, which has `R` on `sensor`. |
| user | `revoke` | `RC` on `sensor` (mutated by the tests). |
| user | `upgrade` | `R` on `switch` (mutated by the tests). |
| user | `b6` | `R` on `sensor` and `switch` (mutated by the tests). |
| user | `b8` | Like `group_r`: `R` on `sensor` only through `readers` (mutated by the tests). |
| group | `readers` | Grants `R` on `sensor`. |

Several tests mutate state that they cannot undo, such as `device-delete` and `remove-from-group`. **Seed fresh data
before every run.**

### Hook actions

| Action | Effect | Stdout |
|---|---|---|
| `set-perm <username> <element_id> R\|RC` | Creates or updates the user's direct grant. | — |
| `revoke <username> <element_id>` | Deletes the user's direct grant. | — |
| `remove-from-group <username> <group>` | Removes the user from the group. | — |
| `element-create <device_id> <points>` | Creates an element on the device. | the new element UUID |
| `element-delete <element_id>` | Deletes the element. | — |
| `device-delete <device_id>` | Deletes the device. | — |
| `key-touch <key_id>` | Re-saves the key (rotation semantics: sockets using it are force-closed). | — |

A hook must return only after the change is committed. Its side effects on live sockets may arrive asynchronously.

### Conventions used by the tests

- **Device auth:** the tests sign their own JWTs with the fixture key and `alg`. The claims are `{id: <device uuid>, exp:
  now+1h}`, and the token is sent as `Authorization: Bearer <jwt>`.
- **Rejected connection:** EITHER a failed handshake (Django returns HTTP 403) OR a close with code `4000` before any
  data frame. A socket that is still open after 1.5 s counts as accepted.
- **Timeouts:** 3 s to receive an expected frame. A negative assertion ("must NOT receive") waits 700 ms. After closing a
  device socket, the tests wait 300 ms.
- **Origin and frame matching:** browser sockets always send the fixture `origin`. Frames are matched by a predicate,
  and unrelated frames are skipped. For example, an `element_connection_status` frame or a history replay frame from an
  earlier test is ignored. Every test sends a unique `message.value`.
- **Ids:** `auth.user_id` is compared after normalizing to a string. Django sends a JSON number; a string is also
  accepted.

### Known-bug mechanism

Tests named `B<n>_...` assert the **fixed** behavior from plan §2.3:

| Bug id | `CONTRACT_KNOWN_BUGS` includes it | Fixed behavior observed? | Result |
|---|---|---|---|
| listed | yes | no | `SKIP: known bug Bn reproduced: ...` |
| listed | yes | yes | `FAIL: bug Bn appears fixed; remove from CONTRACT_KNOWN_BUGS` |
| not listed | no | either | Normal pass/fail |

The run against Django uses `CONTRACT_KNOWN_BUGS=B1,B2,B3,B4,B5,B6,B8`. B7, B9, B10 and B11 have no test.

## Tests

| Test | What it pins |
|---|---|
| `TestDeviceAuth/ValidToken_RS256`, `ValidToken_ES256` | A valid token connects. |
| `TestDeviceAuth/BadSignature`, `AlgorithmMismatch`, `AlgNone`, `ExpiredToken`, `UnknownDevice`, `MissingIdClaim`, `DeviceWithoutKey`, `GarbageToken`, `NoAuthorizationHeader`, `BearerWithoutToken` | The connection is rejected. |
| `TestDeviceAuth/B1_InactiveKey` | [B1] Inactive key → rejected. |
| `TestDeviceAuth/B2_TokenWithoutExp` | [B2] Token without `exp` → rejected. |
| `TestBrowserAuth/NoCookie`, `InvalidSession` | Rejected. |
| `TestBrowserAuth/ValidCookie` | Accepted. |
| `TestBrowserAuth/B4_ForeignOrigin` | [B4] A valid cookie from Origin `http://evil.example` → rejected. |
| `TestSubscribe/ConfirmShape` | The confirm frame is `{type:"subscribe", element_id, subscribed:true, permissions, details, connected:bool}`. `details` equals the fixture value. |
| `TestSubscribe/ReadOnlyPermission`, `PermissionViaGroup` | `permissions:"R"` for a direct grant and for a group-only grant. |
| `TestSubscribe/PermissionDenied`, `UnknownElementDenied` | `{type:"error", error_code:"permission_denied", element_id}`. |
| `TestSubscribe/B5_InvalidElementIdNoTraceback` | [B5] `element_id:"not-a-uuid"` → an `error` frame that contains no `Traceback`. |
| `TestDeviceToBrowser/Delivered` | Device `{element_id, message}` → browser `{type:"message_element", element_id, message, auth:{user_id,username}, last_edit_at}`. The sending socket gets no echo. |
| `TestDeviceToBrowser/AllSubscribersReceive` | Fan-out reaches every subscriber. |
| `TestDeviceToBrowser/OtherSocketOfSameDeviceReceives` | A second socket of the same device gets a device-shaped frame. The sender gets no echo. |
| `TestDeviceToBrowser/NotSubscribedNotDelivered` | Nothing arrives without a subscription. |
| `TestDeviceToBrowser/ForeignElementDropped` | A device that sends to an element it does not own is dropped. The owning device is a positive control. |
| `TestDeviceToBrowser/UnknownElementDropped` | An unknown element, a frame missing keys, or non-JSON is dropped, and the socket keeps working. |
| `TestHistoryReplay` | 7 values are sent to the `points=5` element. A new subscriber gets the confirm, then exactly 5 `message_element` frames with the last 5 values, oldest first. |
| `TestBrowserToDevice/DeliveredToDevice` | An RC browser `{type:"message_element", element_id, message}` → the device gets `{element_id, message, auth:{user_id,username}, last_edit_at}`. The frame has no `type` field and carries the sender's id and username. |
| `TestBrowserToDevice/DeliveredToOtherBrowser`, `NoEchoToSender` | Other subscribers get the message. The sender does not. |
| `TestBrowserToDevice/ReadOnlyUnauthorized`, `NotSubscribedUnauthorized` | `error_code:"unauthorized"` with `element_id`. Nothing reaches the device. |
| `TestBrowserToDevice/UnknownType` | `error_code:"unknown_type"`. |
| `TestBrowserToDevice/InvalidJSON` | `error_code:"invalid_format"`, and the socket stays usable. |
| `TestBrowserToDevice/ExplicitUnsubscribe` | `{type:"unsubscribe", element_id, unsubscribe:true}`, and no further data arrives. |
| `TestSpoofing/B3_DeviceCannotSetActorOrTimestamp` | [B3] A device-supplied `auth.username:"admin"` or `last_edit_at` does not reach browsers. |
| `TestConnectionStatus/ConnectAndDisconnectEvents` | `{type:"element_connection_status", element_id, status:"connected"}`, then `"disconnected"`. |
| `TestConnectionStatus/ConfirmReflectsState` | The confirm's `connected` field is false, then true, then false. |
| `TestConnectionStatus/NoStatusForOtherElements` | Status events reach only subscribers of that device's elements. |
| `TestPermissions/RevokeForcesUnsubscribe` | Revoke → `{type:"unsubscribe", element_id, unsubscribe:true}`. No more data arrives, and a re-subscribe is denied. |
| `TestPermissions/UpgradeSendsPermissionsUpdate` | R→RC → `{type:"permissions_update", element_id, permissions:"RC"}`. The socket can then publish without re-subscribing. |
| `TestPermissions/B6_UnsubscribeKeepsOtherListeners` | [B6] Subscribe A and B, unsubscribe A, revoke B → a forced unsubscribe for B arrives. |
| `TestPermissions/B8_GroupMembershipRemovalForcesUnsubscribe` | [B8] `remove-from-group` → a forced unsubscribe arrives. |
| `TestPermissions/NewGrantAllowsSubscribe` | A grant made while the socket is open is honored on the next subscribe. |
| `TestLifecycle/ElementCreatePropagatesToDevice` | `element-create` and `set-perm` → the live device socket can publish to the new element. Browsers and the device's other sockets receive it, and browser→device works too. |
| `TestLifecycle/ElementDeletePropagatesToDevice` | After `element-delete`, the live device's publishes to that element are dropped, and a subscribe is denied. |
| `TestLifecycle/ElementDeleteForcesSubscriberUnsubscribe` | Deleting an element force-unsubscribes its subscribers. |
| `TestLifecycle/DeviceDeleteClosesSocket` | `device-delete` closes the device socket (code 4000 or 1000, see below), and reconnecting is rejected. |
| `TestLifecycle/KeyTouchClosesSocket` | `key-touch` closes the socket, and the unchanged key still authenticates a new connection. |

## Adjustments to Django's real behavior

The code is the contract. These are the places where the tests follow Django rather than the plan's wording:

1. **Close code for server-forced device closes is 1000, not 4000.** Plan §6.4 says "force-closes the affected sockets
   with code `4000`, as today".
   - **Today:** `key-touch` closes the device socket with **1000**, and so does `device-delete`. Both go through
     `NodeRedConsumer.element_connection_status(status="disconnected")`, which calls `close()` with no code.
   - **Why `device-delete` loses its 4000:** the `close(code=4000)` in `device_updates(state="delete")` exists, but it
     loses the race. Cascading the delete removes the `Connections` row, and that fires "disconnected" first.
   - **Test change:** the tests accept **4000 or 1000**. The Go gateway should send 4000.
2. **`key-touch` only reaches devices that own at least one element.** The notification travels over element groups. The
   fixture gives `rotate` an element for this reason.

## Other observed Django behavior (pinned or worth knowing)

- **Device-originated `auth`:** it is `{"user_id": "coming from Device", "username": "coming from Device"}` unless the
  device spoofs it (B3). The tests check only that the keys are present.
- **Browser→browser relay:** other browsers receive `"last_edit_at": "None"` (a string), because the browser publish
  event carries no timestamp. The device socket gets a server timestamp. In user-originated frames, `auth.user_id` is a
  JSON **number**.
- **History:** only device→server messages go into the history ring. Browser→device messages are not replayed.
- **Publishing:** a browser must be **subscribed** with `RC` before it can publish. Otherwise it gets `unauthorized`.
- **Error frames:**
  - `unknown_type` frames have no `element_id`.
  - `invalid_format` echoes the raw text in `details`.
  - B5 comes back as `error_code:"server_error"` with a 6 KB traceback in `details`.
- **Unsubscribe reason:** a forced unsubscribe adds `"reason": "Permission revoked"`. An explicit unsubscribe has no
  `reason`.
- **Deleting an element:** the permission rows cascade, so subscribers get a forced unsubscribe.
- **Stale "disconnected" events:** `NodeRedConsumer` closes itself on **any** "disconnected" status event for its
  elements. A stale event (one socket of a device closing while another opens) can kill a fresh socket. The suite waits
  300 ms after closing a device socket to avoid this race.
- **Rejected browser sockets:** they log `AttributeError: 'BrowserConsumer' object has no attribute 'subscriptions'`
  from `disconnect()`. This is server-side noise only.

## Running it

| Target | How |
|---|---|
| Go backend | `task contract:go` (needs `task infra:up`). Uses its own database `quack_contract`, reseeds it with `quack dev seed`, and runs `quack serve` on 127.0.0.1:8766. The hook is `quack dev hook`. |
| Go backend under the race detector | `QUACK_BUILD_FLAGS=-race task contract:go`. The run fails if the server log contains a `DATA RACE` report. |
| Load test | `GO_TEST_FLAGS='-tags contract,load -run TestLoad -timeout 10m' bash server/contracttest/go/run.sh`. Knobs: `LOAD_DEVICES`, `LOAD_RATE`, `LOAD_BROWSERS`, `LOAD_DURATION`, `LOAD_IDLE`, `LOAD_OUT`. The results are in `docs/refactor/baseline.md`. |

The Django harness that produced the original baseline (Phase 0) was removed together with the Python code. Every
parity test passed on Django and every bug test reproduced there before the removal. On Go, all tests pass with
`CONTRACT_KNOWN_BUGS` empty.

## Writing a hook for another backend (Go gateway, Phase 4)

1. Seed the same roles and grants, and write a fixture with the schema above.
2. Implement the seven hook actions (an admin CLI or REST calls are fine).
3. Run the suite with the bugs you have fixed removed from `CONTRACT_KNOWN_BUGS`.
