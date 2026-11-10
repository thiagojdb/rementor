# Tailscale routing for personal multi-device access

Research checked 2026-10-04 against Tailscale official documentation plus the current `tailscale/tailscale` source. Cloudflare links are preliminary references: direct documentation fetches returned 403, so those details were not verified in this session. The target model is that each Rementor instance selects a fleet instance whose live route selections it inherits, then applies its own route overrides; phones consume a shared gateway environment. Network ingress provides reachability, while Rementor owns inheritance and override semantics. This is a source review only; no infrastructure was configured. Items marked **Untested** require a proof of concept in the intended app/network.

## Findings

### Stable service names and HTTPS

- A Tailscale Service is a logical service with a MagicDNS name, TailVIP, resource definition, and one or more backend hosts. Clients use the stable service name while Tailscale routes to available hosts. The current setup docs require a supported client version (v1.86.0 or later), an internal resource, and a **tag-based identity** for every host; a user-authenticated device cannot host a Service. **Source:** [Tailscale Services](https://tailscale.com/docs/features/tailscale-services).
- Hosts advertise endpoints and become active after approval, unless the tailnet has a matching auto-approval policy. The admin console supports service/host approval and tag-based policy. The service definition itself can have tags; the docs show policy-based auto-approval of hosts advertising service proxies. **Source:** [Tailscale Services](https://tailscale.com/docs/features/tailscale-services), [Grant syntax and device tags](https://tailscale.com/kb/1018/acls).
- TailVIP/name stability and routing to available hosts are documented. Tailscale advertises built-in traffic steering and high availability; regional routing and in-region balancing have a separate enablement step. The page says the currently supported service transport is TCP. It does not establish app-level HTTP health checking, session affinity, or the exact balancing algorithm. **Untested:** failover timing and balancing behavior for this app.
- **Services can use Serve to terminate HTTPS, with automatic TLS provisioning for the service's tailnet DNS name.** The dedicated Services setup example runs `tailscale serve --service=svc:web-server --https=443 127.0.0.1:8080`; its output URL is `https://web-server.<tailnet-name>.ts.net:443/`, and the docs state that Serve automatically provisions a TLS certificate for the unique tailnet DNS name. This is distinct from a device's Serve hostname: the service host advertises for the service identity/name. **Sources:** [Tailscale Services setup](https://tailscale.com/docs/features/tailscale-services), [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve), [HTTPS certificates](https://tailscale.com/docs/how-to/set-up-https-certificates).
- The tailnet HTTPS feature requires enabling HTTPS in the admin DNS settings; Tailscale notes that machine and tailnet DNS names are published in public certificate transparency logs. The docs describe `ts.net` service naming, not arbitrary custom-domain provisioning through Services/Serve. A custom domain would need a separate DNS/TLS/proxy arrangement. **Sources:** [HTTPS certificates](https://tailscale.com/docs/how-to/set-up-https-certificates), [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve).
- **Untested:** the desired UI/API hostname set, admin prerequisites in the intended tailnet, browser certificate behavior, and whether service HTTPS URLs fit the app's URL/cookie model. Tailscale Services documents TCP transport endpoints; the Serve host configuration can provide HTTPS and proxy to HTTP locally. Do not infer app-level routing or domain aliasing from the TailVIP alone.

### Identity through Serve and ways to identify a node

- Serve's HTTP reverse proxy removes incoming `Tailscale-User-*` headers before adding its own, specifically to prevent spoofing. The documented headers represent the requesting **user profile** (`Tailscale-User-Login`, `Tailscale-User-Name`, and profile picture), not a unique device/node identifier. They are not populated for tagged-device requests. The Serve implementation additionally skips the headers when it cannot resolve a qualifying user identity (including Funnel requests). **Sources:** [Serve identity headers](https://tailscale.com/kb/1312/serve), [implementation: `addTailscaleIdentityHeaders`](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go).
- Therefore, do **not** use Serve's HTTP headers to authorize one particular client device. In the implementation, Serve derives the headers using `WhoIs` on the original incoming connection address, then emits user login/name/profile data. It does not emit the source node identity as a `Tailscale-*` header.
- A backend that needs the exact source node can use Tailscale's local API `WhoIs` with the peer source IP (or IP:port) to resolve node metadata. Tailscale's `tsnet` example uses the incoming HTTP request's `RemoteAddr` with `WhoIs`. This is reliable only when that peer address still represents the originating Tailscale client. **Sources:** [`client/local` WhoIs implementation](https://github.com/tailscale/tailscale/blob/main/client/local/local.go), [`WhoIs` API](https://pkg.go.dev/tailscale.com/client/local#Client.WhoIs), [`tsnet` example](https://github.com/tailscale/tailscale/blob/main/tsnet/README.md).
- Serve HTTP termination/proxying creates a new backend HTTP request, so the backend sees the Serve process as its network peer; use the trusted Serve-injected user headers for user-level identity, not the backend socket peer as the original client. For TCP forwarding, the connection remains a TCP stream. Serve's optional PROXY protocol v1/v2 documents passing the original client IP/port through a proxy; a backend can use that source address with `WhoIs` if it is a Tailscale address. **Sources:** [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve), [TCP forwarder / PROXY protocol](https://tailscale.com/kb/1312/serve).
- Tailscale warns that identity/capability headers are trustworthy only when the backend cannot be reached directly by untrusted callers; otherwise callers can supply forged headers themselves. It recommends binding the backend to localhost when relying on them. Do not expose the backend listener to LAN/public traffic, and do not trust user-supplied `Tailscale-*` headers except after the trusted Serve boundary. **Source:** [Serve identity and capability headers](https://tailscale.com/kb/1312/serve).
- For node identity, prefer a backend that receives a real Tailscale peer address and performs `WhoIs`, or TCP forwarding that preserves the source and is verified in the actual topology. **Untested:** whether a proposed multi-hop proxy/gateway retains that address, and whether PROXY protocol reaches the final application unchanged.

### DNS, browsers, and app constraints

- MagicDNS supplies names for devices; Serve's docs restrict its own names to the tailnet `ts.net` namespace. Tailscale Services supplies its own stable MagicDNS name and TailVIP. These do not document arbitrary public/custom-domain hosting by Serve. **Sources:** [MagicDNS](https://tailscale.com/docs/features/magicdns), [Tailscale Services](https://tailscale.com/docs/features/tailscale-services), [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve).
- If separate UI/API origins are used, browser same-origin rules apply and the app/API must deliberately configure CORS, cookies, and credential behavior for those origins. Tailscale's routing/Serve docs do not promise to configure app CORS. **Untested:** the current app's CORS, websocket, cookie, and redirect behavior across proposed names; validate them in the PoC.
- Neither Tailscale Services nor Cloudflare Tunnel/WARP supplies Rementor's live selection inheritance or per-instance overrides. They provide network paths/private ingress; route choice, inheritance source selection, and local override precedence remain Rementor application behavior. Phones can consume a shared gateway environment through a reachable private ingress, subject to their network client/enrollment and app origin setup (Cloudflare finding below).

## Recommended acceptance test before choosing a provider

Core acceptance should validate the inheritance model, not per-device identity at ingress: start two Rementor instances with distinct live route selections; select instance B as A's inheritance source; confirm A observes B's current selection, then apply an A-local override and confirm it wins only in A while B remains unchanged. Change B's selection live and verify the documented/desired propagation behavior. Connect a phone to the shared gateway environment and confirm it reaches the expected UI/API over HTTPS, including cookies, CORS, redirects, and WebSockets. Test gateway restart and a lost fleet instance to establish recovery behavior. Repeat ingress reachability using each provider candidate without changing the application-level source/override semantics.

Treat exact source-node identification as an optional future hardening optimization, not a gate for this core feature. If pursued later, separately test two client devices for `WhoIs` results through the chosen ingress, tagged-device behavior, forged HTTP identity headers, direct-backend access, and source address preservation through any TCP/PROXY-protocol proxy. Serve HTTP headers remain user identity only and cannot meet per-node identification by themselves.

### Cloudflare private ingress candidate (unverified in this session)

The following summarizes the candidate arrangement to investigate, with official
reference links. Confirm current support and requirements before choosing it;
these pages could not be fetched in this session.

- Cloudflare Tunnel creates an outbound connection from `cloudflared` to Cloudflare and can publish private-network routes by IP/CIDR; Cloudflare One Client/WARP can connect enrolled devices to those private routes. Tunnel alone does not make a private origin reachable to arbitrary phone browsers: the phone needs WARP enrollment/client, a route and policy that allow the destination, and private DNS when using a private hostname. Cloudflare documents mobile WARP clients (including iOS and Android) and device enrollment/profile configuration. This supports the possibility of phone-to-private-gateway network access without exposing the gateway as a public origin. **Sources:** [Cloudflare private network routes](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/private-net/), [WARP client overview](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/), [device enrollment](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/device-enrollment/), [manual deployment](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/manual-deployment/), [WARP on iOS](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/manual-deployment/ios/), [WARP on Android](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/manual-deployment/android/).
- Cloudflare Tunnel can also publish HTTP(S) applications behind `cloudflared` using hostname-based public application routes/access policies; private network routing instead routes enrolled WARP clients to private IP space. These are distinct ingress patterns. For a phone consuming a private HTTP/HTTPS gateway, validate private route advertisement, WARP enrollment/profile, DNS name resolution and TLS certificate trust for the chosen private hostname. **Sources:** [Private network routes](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/private-net/), [Cloudflared private-network setup](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/private-net/cloudflared/), [Tunnel public hostnames](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/routing-to-tunnel/), [WARP client overview](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/), [device enrollment](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/device-enrollment/), [iOS](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/manual-deployment/ios/), [Android](https://developers.cloudflare.com/cloudflare-one/connections/connect-devices/warp/deployment/manual-deployment/android/).
- These docs describe network connectivity and access policy; they do not describe inheritance of live application route selections between Rementor instances. That remains application state and precedence logic in Rementor. **Untested:** the actual mobile WARP profile/enrollment UX, private DNS/TLS arrangement, phone browser behavior, and ingress performance in the target deployment.

## Source snapshot and confidence

Tailscale official pages were fetched on 2026-10-04. Cloudflare docs were inaccessible to direct fetch (403) during this review, so confirm those links and details against live official docs before deployment. Source behavior was checked in Tailscale's `main` branch files linked above. Public docs establish the stated configuration and header semantics; no live tailnet, client matrix, browser flow, or failover test was run. Tailscale's source `WhoIs` path and Serve proxy behavior are implementation evidence, not a guarantee that arbitrary additional proxies preserve peer addresses.

## Live provider proof — 2026-10-04

Tested on Marcus with Tailscale **1.102.4**, using temporary Serve HTTP proxies
on two unused HTTPS ports and a backend bound exclusively to loopback. The
existing Serve configuration was preserved. Both test proxies were removed
when the proof finished.

Observed behavior:

- Serve overwrote forged and duplicate `X-Forwarded-For` values with the actual
  Tailscale source address, and overwrote a forged `X-Forwarded-Proto` with
  `https`. A forged `Tailscale-User-Login` did not survive.
- `tailscale whois --json` on that source address returned Marcus's stable node
  identity. The backend's socket peer was loopback, as expected. User-profile
  headers alone still cannot identify a device.
- A real Chromium browser accepted the Serve certificate without bypassing TLS
  validation. An unchanged HTML/JavaScript fixture fetched a runtime origin map,
  called a separate HTTPS API origin with credentials, returned a Secure/HttpOnly
  cookie, followed a redirect, and echoed a message over WSS. Browser errors were
  empty. This was a fixture, **not a GISS login test**.

This proves a candidate **node Serve adapter** can recover the requesting node
using Serve's source-address header and local WhoIs on this installed version.
The eventual adapter must use a dedicated loopback data-plane backend, validate
one source address, reject unknown WhoIs results, authorize the resolved identity,
and explicitly bind identities to Rementor instances. A directly accessible
backend or another HTTP proxy inserted before that boundary invalidates this
proof. Do not infer trust in arbitrary forwarded headers, user names, or cookies.

Only Marcus requested the proof page. Distinguishing Marcus from Brutus or a
phone remains untested because those devices were unavailable. Tagged clients,
Tailscale Services/TailVIP host approval, multiple ingress replicas, and gateway
failover were not tested. Node Serve's computer-bound hostname also does not
establish a hostname that survives replacement of its ingress computer; that
still requires the Services or another DNS/TLS provider proof.

Separately, `TestPrivateNetworkAcceptance` passed with
`REMENTOR_ACCEPTANCE_PRIVATE_ADDRESS` set to Marcus's physical LAN address. Two
isolated Rementor instances connected through that address with certificate-pinned
TLS enrollment, authenticated snapshots, declared-owner forwarding, management
RPC rejection and revocation. This used neither a Tailscale address nor a tunnel,
but both instances ran on Marcus; it is **not cross-host LAN acceptance**.

Run that opt-in test on a configured machine with:

```sh
REMENTOR_ACCEPTANCE_PRIVATE_ADDRESS=<this-machine-LAN-IP> \
  go test ./internal/peers -run TestPrivateNetworkAcceptance -count=1 -v
```

These results add live evidence to the earlier source review. They do not make
all items marked untested above complete.


## Physical-host peer transport proof — 2026-10-05

Marcus and Brutus ran isolated instances of the deployed build with independent
identities and fixture applications. Tailscale pairing, actual nginx owner
forwarding, automatic source-change propagation, receiver override preservation,
source outage/cache behavior, unavailable-owner 502, restart identity recovery,
WebSocket echo and revocation passed. Offline stop-following restored the receiver's
original registrations.

The same isolated identities were paired again through physical LAN interfaces.
Routing used Marcus's Ethernet and Brutus's Wi-Fi, without a tunnel or Tailscale
address. Certificate-pinned pairing, authenticated synchronization, HTTP and
WebSocket forwarding, and revocation passed. Brutus required a temporary firewall
rule scoped to Marcus and the test listener; the rule was removed. All fixture
services and temporary Serve mappings were removed, and the original Serve
configuration was checked unchanged.

This closes the earlier single-host limitation for peer transport. It does not
prove public-ingress selection from Brutus or a phone, real GISS authentication,
stable HTTPS identity transfer, or equivalent gateway replicas.


## Physical-host public ingress and GISS bootstrap — 2026-10-05

The same Serve HTTPS manifest URL selected different configured environments for
Marcus and Brutus. Both use Tailscale 1.102.4. Marcus's local WhoIs response
contains `MachineAuthorized: true`; Brutus's live peer response omits that field.
Interpreting omission as denial incorrectly rejected Brutus. The resolver now
rejects an explicit false value, unknown nodes, mismatched source ownership, the
`Expired` flag, and elapsed key expiry, while authorizing the current-netmap identity
against the saved account/device policy. Tailscale's WhoIs implementation resolves
addresses and node IDs from its current network map, rather than an HTTP user
header: [WhoIs source](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnlocal/local.go).
The Node struct uses `omitempty` for `MachineAuthorized`: [Node source](https://github.com/tailscale/tailscale/blob/v1.102.4/tailcfg/tailcfg.go).

A real GISS artifact generated by the configured devctl lifecycle loaded the
runtime script before Angular on a valid Serve HTTPS frontend origin. Its
`URL_BASE` resolved a separate configured API origin, and municipality lookup
resolved the selected routing hostname's `dev` label. Real portal initialization
requests returned 200 across those origins. The configured logical account then completed the real browser login and test
client-profile selection, reached `/operacao`, and received successful permissions
and subsequent authenticated API responses through the separate HTTPS API origin.
Initial token failures were caused by the isolated nginx harness dropping headers
with underscores; adding the production renderer's existing
`underscores_in_headers on` setting resolved them. No application authentication
behavior was bypassed or changed. Brutus browser authentication and phone access
remain separate checks.


## Shared ingress protocol acceptance — 2026-10-05

The shared selector, durable transfer/recovery, and pinned replica data plane are
implemented independently of a DNS/HTTPS adapter. Selector ownership moves while
the routing computer and policy remain fixed. A receiving successor independently
fetches the predecessor's committed record; replicas cannot promote themselves
through synchronization. Gateways and routing hosts capture exact policy digests,
check shared serving roles, independently resolve the original Tailscale source,
and verify response proofs. Owner outages fail explicitly.

Three independent paired identities and SQLite stores passed HTTP and WebSocket
routing through real nginx, interrupted activation, restart, revocation and stale
proof checks. A browser drove create/join/transfer on two isolated daemons and
proved an open form retains its original concurrency version across polling.
These instances were on Marcus, so this establishes protocol behavior rather
than physical gateway or Services acceptance. Brutus was offline for this new
round. Its earlier physical GISS login and LAN/Tailscale peer proofs remain valid.

No Services adapter has been committed on the basis of these tests. Stable
service-name/DNS/certificate movement still requires the dedicated tagged-provider
proof, preserving the physical hosts' existing user identities. Real phone
acceptance still awaits the requested check at the isolated GISS HTTPS origins.


## Physical shared gateways and current remaining proof — 2026-10-05

The latest build is installed on both Marcus and Brutus. Fresh isolated identities,
real nginx instances and temporary HTTPS Serve entries on both computers passed
shared HTTP response proofs, WebSocket forwarding, selector transfer with a fixed
routing computer, persisted restart, unavailable-selector rejection, and live
WebSocket cancellation on pairing revocation. A workspace-ID collision on Brutus
did not change the selected Marcus application. Both original Serve configurations
were verified unchanged after removing the temporary entries. This closes the
physical shared-protocol gap recorded in the preceding checkpoint.

It does not establish a single DNS/HTTPS service identity across gateway movement:
the test used the two existing physical hostnames. The Services admin page still
redirects to sign-in. Provisioning the dedicated tagged test gateways and verifying
service-name/certificate movement must precede a committed Services adapter.

The real GISS build remains available at port 18448 with its separate API origin
at port 18449. Current native-browser inspection shows the authenticated operation
page, a successful origin manifest, and Qualidade selection; the frontend also
responds over HTTPS. Marcus/Brutus browser acceptance is complete. A real phone
login/profile selection and successful authenticated API traffic remain unproved.
