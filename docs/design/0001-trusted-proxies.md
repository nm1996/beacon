# 0001. Client address behind trusted proxies

Status: Proposed

## Context

Rate limits key unauthenticated registrations and failed authentication by the client network: the IPv4 address, or IPv6 `/64`, of the TCP connection. Behind a load balancer or reverse proxy, every connection comes from the proxy, so all clients share one key. One client that sends bad tokens then puts every client behind the proxy over the failure limit.

Proxies pass the original address in `X-Forwarded-For`, but anyone can set that header. Trusting it blindly lets a client pick its own key, either to escape its limit or to use up someone else's.

## Decision

A new setting, `server.trusted_proxies`, lists CIDR ranges, for example `["10.0.0.0/8", "fd00::/8"]`. It is empty by default, which keeps today's behavior.

To find the client address of a request:

1. Start with the connection's address. If it is not in a trusted range, that is the client, and headers are ignored.
2. Otherwise read `X-Forwarded-For` from right to left. Combine every instance of the header in order, as RFC 7230 requires. Skip each address in a trusted range. The first address that is not trusted is the client.
3. If the header is missing, or an entry cannot be parsed before an untrusted address is reached, the connection's address is the client. If every entry is trusted, the leftmost one is: the request started inside the trusted network.

This is the "rightmost untrusted" rule that nginx (`real_ip_recursive on`), Envoy (`xff_num_trusted_hops`, counted rather than matched) and AWS ALB use. Entries left of the first untrusted hop were written by the client and are never believed.

The resulting address feeds `clientNetwork`, so the rate limit keys, and the `network` in refused-request logs, follow it. `RemoteAddr` is not rewritten, so nothing else changes meaning.

## Alternatives considered

- **A hop count, as in Envoy.** It is simpler to configure but breaks silently when the topology changes, for example when a CDN is added in front. CIDRs say *who* is trusted, which survives topology changes, and they are what operators already know from nginx.
- **RFC 7239 `Forwarded`.** It is the standard, but few load balancers emit it and its syntax (quoted IPv6, obfuscated identifiers) adds parsing risk. It can be added later behind the same setting.
- **PROXY protocol.** It is the right answer for TCP load balancers and is out of scope here. It replaces the connection address before HTTP is even parsed, so it would compose with this design.

## Consequences

- Operators behind a proxy get per-client limits by listing the proxy's ranges.
- A wrong, too wide range, such as `0.0.0.0/0`, lets clients spoof their address. The README warns about it, and Beacon logs the configured ranges at startup.
- The cost per request is a header parse only when the connection comes from a trusted proxy.

## Open questions

- Should `/health` and metrics report the derived address? Default: no; only rate limits and logs use it.
