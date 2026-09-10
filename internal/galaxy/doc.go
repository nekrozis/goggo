// Package galaxy speaks the GOG Galaxy content API: the endpoints that serve
// build, manifest and depot data for a product, authenticated with the OAuth
// access token rather than with the website session cookie.
//
// Responsibilities and boundaries:
//
//   - It is a library layer. It returns errors and never prints or exits. The
//     C++ source (galaxyapi.cpp) prints a message and hands back an empty
//     Json::Value on failure; the port returns an error instead. The recorded
//     differences are in dev/audit/S13.md.
//   - It owns no transport: the caller builds one *httpx.Client and passes it
//     in, so the retry policy, timeouts, the low-speed guard and the
//     User-Agent are configured in exactly one place. It never refreshes the
//     token either — it only reads the store, and refreshing stays with
//     internal/auth and the caller.
//   - The access token is attached as "Authorization: Bearer" only while the
//     store reports it unexpired. An expired or empty token means the request
//     goes out without that header, exactly like the C++ source
//     (galaxyapi.cpp:98-105).
//
// S13 covers the builds entry point (getProductBuilds) and the request
// primitive it needs. Manifests (S14), secure links, CDN resolution and depot
// items (S15), and product details with file lists (S17) are ported later.
package galaxy
