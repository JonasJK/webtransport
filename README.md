# cursors

Real-time multiplayer cursor sharing over WebTransport — HTTP/3 and QUIC, lower
latency than WebSockets.

**[→ Try it live](https://webtransportdemo.duckdns.org/)**

<p class="ciu_embed" data-feature="webtransport" data-periods="future_1,current,past_1,past_2" data-accessible-colours="false">
  <picture>
    <source type="image/webp" srcset="https://caniuse.bitsofco.de/image/webtransport.webp">
    <source type="image/png" srcset="https://caniuse.bitsofco.de/image/webtransport.png">
    <img src="https://caniuse.bitsofco.de/image/webtransport.jpg" alt="Data on support for the webtransport feature across the major browsers from caniuse.com">
  </picture>
</p>
<script src="https://cdn.jsdelivr.net/gh/ireade/caniuse-embed/public/caniuse-embed.min.js"></script>
---

## How it works

Pointer positions are sent to a Go server as raw 5-byte QUIC datagrams every ~33
ms and fanned out to all other connected clients at 20 Hz. The server coalesces
rapid updates rather than forwarding every packet, keeping bandwidth flat under
fast movement.

The frontend interpolates peer cursors each frame, draws Catmull-Rom spline
trails, and casts soft radial lighting per cursor. Clicks and disconnects
trigger a shockwave that physically pushes the dot grid.

## Stack

- **Backend** — Go, [`quic-go`](https://github.com/quic-go/quic-go),
  [`webtransport-go`](https://github.com/quic-go/webtransport-go), Let's Encrypt
  via `autocert`
- **Frontend** — Vanilla JS, Canvas API
