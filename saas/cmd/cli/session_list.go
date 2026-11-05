// session_list.go — the tool session id redaction whoami and `lore who`
// share (AGENT_IDENTITY 9, OV11-f). `lore session list` itself is the
// operation session.list (ops/session over sessionHost, P3-RUNS-4).
package main

// redactToolSession hides a raw tool session id unless stdout is a
// terminal and CI is unset (section 9).
func redactToolSession(id string) string {
	if id == "" || !quietOutput() {
		return id
	}
	return redactedRepo
}
