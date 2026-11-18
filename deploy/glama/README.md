# `deploy/glama/` - the build behind the Glama listing

[Glama](https://glama.ai/mcp/servers/BRF-Tech/filex) lists filex as an MCP
server and checks it by building an image, starting it and asking the server
for its tools over stdio, through its own `mcp-proxy`. filex's MCP server is
not a stdio program: it is part of the filex server, at `/api/ai/mcp`, behind
an API key ([MCP endpoint](../../docs/MCP.md#mcp-endpoint-apiaimcp)). These
files give Glama's image one to talk to: a filex of its own, listening on
127.0.0.1 inside the container, with an empty storage and one administrator
whose random password never leaves the start script.

| File | What it does |
|---|---|
| [`build.sh`](build.sh) | Glama's only build step. Downloads the filex release that `web/package.json` names, for the machine's architecture, checks it against that release's `checksums.txt`, and installs it with the two files below. |
| [`filex-mcp-stdio`](filex-mcp-stdio) | The command Glama starts. Runs a throwaway filex in the container (its own data and one local storage under `/tmp`, an administrator with a random password, no update checks, no thumbnails), signs in, makes an API key with the scopes `read,write,delete,comments:rw,mcp`, and hands over to the bridge. |
| [`filex-mcp-bridge.cjs`](filex-mcp-bridge.cjs) | Reads one JSON-RPC message per line on stdin, posts it to `http://127.0.0.1:5212/api/ai/mcp` with that key, and writes the answer to stdout. Glama refuses `mcp-remote` in a build step, so the bridge is our own. |
| [`Dockerfile`](Dockerfile) | The image Glama builds, with this repository copied in instead of cloned, for trying a change before it is pushed. |

The tools Glama lists are the ones a key with those scopes is offered: every
file tool, and none of the `admin_*` tools, which need the `admin` scope.

## The settings on Glama

Admin → Dockerfile on the listing. Glama generates its Dockerfile from these
fields; it does not read them from the repository (`glama.json` carries only
the maintainers).

| Field | Value |
|---|---|
| Base image | `debian:trixie-slim` |
| Node.js version | `24` |
| Build steps | `["sh deploy/glama/build.sh"]` |
| CMD arguments | `["mcp-proxy", "--", "/usr/local/bin/filex-mcp-stdio"]` |
| Environment variables JSON schema | `{"type": "object", "properties": {}}` |
| Placeholder parameters | `{}` |

Glama clones the repository at its newest commit, so a release reaches the
listing the next time it builds: **Build & Release** on that page.

## Trying it locally

From the repository root:

```sh
docker build -f deploy/glama/Dockerfile -t filex-glama .
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"try","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' |
  docker run -i --rm filex-glama filex-mcp-stdio
```

The second answer lists the tools. `docker run --rm -e MCP_PROXY_DEBUG=true
filex-glama` starts the image the way Glama does, with `mcp-proxy` in front.
