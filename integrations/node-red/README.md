# node-red-contrib-quackquack

Node-RED nodes for [Quack Quack](https://github.com/taha2samy-3/qauk-qauk), a real-time IoT dashboard platform.
Send sensor readings to live dashboards, and receive the commands people send from them (switches, sliders).

![Node-RED next to a live Quack Quack dashboard](https://raw.githubusercontent.com/taha2samy-3/qauk-qauk/main/docs/imgs/screenshots/nodered-with-dashboard.webp)

| Node | What it does |
|---|---|
| **quack-device** (config) | One device on a Quack Quack server: address, device ID, private key. All nodes that use it share one connection. |
| **quack out** | Sends `msg.payload` to an element (a sensor, a chart series, a switch's state). |
| **quack in** | Emits the commands that dashboards send to the device's elements. |

What the nodes handle for you:

- **Tokens.** A fresh JWT is signed with the device key for every connection, so it never expires on you.
  ES256 or RS256 is picked from the key.
- **Reconnecting** with backoff. The nodes also notice a silent server (no ping in 75 s), and slow down when the key is refused or the device is revoked.
- **Elements by name.** Pick them from a list in the editor, or route with `msg.topic = "Climate"`.
- **Loud errors** instead of silent drops: offline, unknown element, a frame over 64 KiB, over the device's rate, or
  over an element's rate limit (the server lists each element's limit; elements set to "keep latest" aren't limited
  here, because the server keeps their newest value).
  The errors can be caught with a *catch* node.
- **Key pairs.** The device config can generate one; copy the public key into the platform.

## Install

The package is released on GitHub (not on the npm registry). Each [release](https://github.com/taha2samy-3/qauk-qauk/releases) tagged `node-red-v…` has the tarball attached. Install it in the Node-RED user directory (usually `~/.node-red`), then restart Node-RED:

```sh
cd ~/.node-red
npm install https://github.com/taha2samy-3/qauk-qauk/releases/download/node-red-v0.2.0-alpha.1/node-red-contrib-quackquack-0.2.0-alpha.1.tgz
```

Or download the `.tgz` from the release and upload it in Node-RED: **Menu → Manage palette → Install → upload** (the icon next to the search box).

Then restart Node-RED. The nodes appear in the **quack quack** palette category. Requirements: Node-RED 3.0 or later, Node.js 18 or later.

## Set up a device

1. **On the Quack Quack server**, as an admin:
   1. **Admin → Keys**: add the device's *public* key. You don't have one yet? Open a *quack-device* node in
      Node-RED and press **Generate key pair**. It fills in the private key and shows the public key to copy.
   2. **Admin → Devices**: create the device and assign that key. Copy its ID.
   3. **Admin → Elements**: create the device's elements, for example `Climate` and `Gate relay`.
   4. **Admin → Permissions**: give users (or groups) access to the elements. Dashboards show only elements their viewer may read (`R`) or control (`RC`).
2. **In Node-RED**, add a *quack out* or *quack in* node and create its device:
   - **Server**: the platform's address, e.g. `http://127.0.0.1:8080` or `https://iot.example.com`
   - **Device ID**: from step 1.2
   - **Private key**: paste it, generate it, or give a **key file** path (handy for Docker secrets)
3. Press **Test connection**. It should list the device's elements. Then deploy.

![The device configuration with a successful connection test](https://raw.githubusercontent.com/taha2samy-3/qauk-qauk/main/docs/imgs/screenshots/nodered-device-config.webp)

## Sending: quack out

| `msg.payload` | sent as `message` |
|---|---|
| `21.5`, `true`, `"ON"` | `{"value": 21.5}`, `{"value": true}`, `{"value": "ON"}` |
| `{"temperature": 21.5, "humidity": 40}` | unchanged: dashboard widgets can bind to any attribute, even nested ones (`gps.lat`) |

Choose **send msg.payload unchanged** to skip the `{"value": …}` wrapping.

The element is the one picked in the node. If none is picked, it comes from `msg.element_id`, then from `msg.topic`.
Both accept an element ID or name, and names are matched without regard to case. An element an admin adds while
you're connected works right away.

## Receiving: quack in

A dashboard switch turned on arrives as:

```js
{
  payload: 1,                  // {"value": 1} unwrapped (choose "unchanged" to keep the object)
  topic: "Gate relay",         // the element's name
  element_id: "01a117df-…",
  from: { kind: "user", user_id: 1, username: "admin" },
  last_edit_at: "2026-10-07T19:48:07.120Z"
}
```

**Confirm commands.** Dashboards wait for the device to report the new state. Apply the command, then send it
back through a *quack out* with no element picked. It replies to `msg.element_id`. Or tick **Confirm commands**
to echo every command right away.

![The quack in node with elements loaded from the server](https://raw.githubusercontent.com/taha2samy-3/qauk-qauk/main/docs/imgs/screenshots/nodered-in-node.webp)

## Example

**Menu → Import → Examples → node-red-contrib-quackquack → send-and-receive** imports a flow with two parts:
a sensor reading sent every 5 s, and commands applied and confirmed. Set the device, then deploy.

## Status and errors

| Badge | Meaning |
|---|---|
| 🟢 connected | Ready |
| 🟡 connecting | Opening the connection |
| 🔴 disconnected, retry in N s | The network or the server went away; retrying with backoff |
| 🔴 auth failed | HTTP 403: wrong device ID or key, key inactive, or no key assigned to the device. Retries every minute. |
| 🔴 device deleted / key removed | The device was revoked by an admin (close code 4000) |

Nothing is queued while offline. Each message sent while offline is an error, so you can catch it and buffer it if you need to.

## Error codes

`msg.error` / the *catch* node gets a code in `error.code`:

| Code | Meaning |
|---|---|
| `offline` | Not connected |
| `element` / `ambiguous` | Unknown element, or two elements share the name |
| `size` | Frame over 64 KiB |
| `json` | Not JSON-serializable |
| `rate` | Over the device's **Max rate** (default 500/s, the server's device limit) |
| `element-rate` | Over the element's limit set by the server admin (default 50/s), for elements that drop what's over |

## Protocol

The nodes speak the platform's device WebSocket protocol, and use `GET /device/elements` (same token) to list
elements. See the [device API reference](https://github.com/taha2samy-3/qauk-qauk/blob/main/docs/04_api_reference/device_api.md).

## Development

```sh
task nodered:test    # unit tests + the nodes inside a real Node-RED runtime
task nodered:dev     # a local Node-RED on http://127.0.0.1:1880 with these nodes loaded
task nodered:pack    # the npm tarball
```

## License

MIT
