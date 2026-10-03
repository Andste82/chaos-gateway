// Package api is the REST API server (plan §2.15, §2.16): the handlers of the operations of
// api/openapi.yaml that exist in this build, on the generated Gin interface. Everything else in the
// spec answers 422 `unsupported_feature` with the milestone that brings it.
//
// The server holds no state of its own about the gateway: the engine owns it (snapshot, apply,
// events), the store keeps the revisions, the auth store the credentials and the audit log the
// history. What the server adds is the protocol: problem+json errors, authentication with scopes,
// CSRF for sessions, ETag and If-Match, JSON Merge Patch candidates, idempotency keys, Server-Sent
// Events with replay, and the audit trail of every write.
package api
