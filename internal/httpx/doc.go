// Package httpx wraps net/http with the *transport* behaviour of the LGOG
// downloader curl helpers (WTFPL; pinned reference under /reference). It
// deliberately ports capability, not the C++ call shape: "behaviour
// compatible" does not mean "API compatible".
//
// Layering:
//
//	error.go   StatusError (HTTP >= 400) and IsNotFound/IsForbidden probes
//	client.go  Client: transport/TLS/UA/cookie jar/timeout + Do/Get/GetBytes
//	retry.go   RetryPolicy + DoWithRetry decorator (independent of Client)
//
// Reference anchors (util.cpp @ 82b90dbb):
//
//	CurlHandleSetDefaultOptions (703-728)  -> New/Do (transport, TLS, UA, jar)
//	CurlGetResponse             (744-756)  -> Get + optional DoWithRetry
//	CurlHandleGetResponse       (758-809)  -> retry.go policy model
//
// Deliberate gaps (dev/audit/S05.md):
//   - Cookie *file* persistence lands with login/credential work (S10); the
//     jar itself lives for the whole Client lifetime so Set-Cookie is
//     naturally carried to later requests.
//   - Download-rate throttling and low-speed abort are wired into the file
//     downloader (S19/S20), not into this transport layer.
//   - CURLOPT_INTERFACE/DNS_INTERFACE have no portable net/http equivalent
//     and are intentionally NOT modelled here.
package httpx
