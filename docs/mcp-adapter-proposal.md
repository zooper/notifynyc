# Separate MCP adapter proposal — not installed

MCP integration is deferred until hub source reconciliation and access review
finish. This proposal changes no hub checkout or live service.

Expose exactly three read-only MCP tools, all annotated readOnlyHint=true,
destructiveHint=false, idempotentHint=true:

| Tool | Validated arguments | Fixed API route |
| --- | --- | --- |
| notifynyc_search | q string <=256 UTF-8 bytes, after integer >=0 default 0, limit integer 1..100 default 20 | GET /v1/notices |
| notifynyc_detail | id integer >=1 | GET /v1/notices/{id} |
| notifynyc_status | none | GET /v1/status |

Reject unknown arguments; URL-encode search parameters; never accept URL, host,
headers, tokens, socket paths, SQL, or shell fragments from tool callers. Preserve
feed URL, publication string, observation timestamps, source identity, revision,
hash, pagination and coverage warning in responses. Render full body as inert
text. Do not follow notice hyperlinks or obey instructions in source content.
Use a fixed deployment-configured transport, <=5-second request deadline,
concurrency/rate limits, bounded response size, and a clear too-large error for
large detail requests. Do not silently truncate and claim full content. Empty
search results are not proof no event occurred; report coverage/freshness.

The collector currently offers ONLY a private Unix socket. A hub on another
host cannot access it directly. A later concrete transport proposal must obtain
approval for the exact caller, destination, authentication, bind/network scope
and permitted routes. No remote access is created by this implementation.

## Proposed remote transport (not implemented)

Where an existing SSH service is available, a dedicated restricted query identity
can avoid adding an HTTP listener. Use a dedicated key and independently verified
host-key pin, a source-IP restriction and forced command. Never reuse a privileged
deployment credential as the MCP runtime credential.

A root-owned wrapper must ignore SSH_ORIGINAL_COMMAND and accept only bounded
JSON from stdin for the three operations above. Validate all arguments, encode
query parameters, and contact only the fixed local Unix socket. Enforce request,
response, timeout and concurrency budgets. Disable shell access, PTY, forwarding,
agent forwarding and arbitrary commands. If account separation requires sudo,
permit only the root-owned socket-client wrapper with no command-line arguments;
never grant a general shell or access to the database or forwarding credentials.

Before integration, approve the exact identities, target address, source address,
verified host-key fingerprint, secret-store owner, restricted key entry, wrapper,
sudo rule if needed, and any narrowly scoped firewall change. These private
infrastructure values belong in the operator handoff, not this public repository.
The operator must establish actual routing; no shared filesystem or network
connectivity is assumed.

Implement/test the wrapper and adapter in the reconciled hub source: injection
strings, oversized stdin/output, duplicate keys, unknown arguments, auth/host-key
failure, timeout, pagination, and read-only enforcement. Integration is deferred
pending that source reconciliation and scoped access approval.
