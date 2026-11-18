"use strict";
// stdio <-> filex's MCP endpoint (deploy/glama/README.md). One JSON-RPC
// message per line on stdin is posted to /api/ai/mcp, which answers each
// request with one JSON body (stateless, JSON-response streamable HTTP) and a
// notification with 202 and no body; answers go to stdout one per line, in
// the order the messages came. Glama refuses mcp-remote in a build, so this
// small bridge is ours.
const ENDPOINT = "http://127.0.0.1:5212/api/ai/mcp";
const headers = {
  "Content-Type": "application/json",
  Accept: "application/json, text/event-stream",
  Authorization: "Bearer " + process.env.FILEX_MCP_TOKEN,
};
delete process.env.FILEX_MCP_TOKEN;

const out = (msg) => process.stdout.write(JSON.stringify(msg) + "\n");
const fail = (msg, why) => {
  if (msg && msg.id !== undefined && msg.id !== null) {
    out({ jsonrpc: "2.0", id: msg.id, error: { code: -32603, message: String(why) } });
  }
};

let queue = Promise.resolve();
require("readline")
  .createInterface({ input: process.stdin })
  .on("line", (line) => {
    if (!line.trim()) return;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }
    queue = queue.then(async () => {
      try {
        const res = await fetch(ENDPOINT, { method: "POST", headers, body: line });
        const body = (await res.text()).trim();
        if (body) out(JSON.parse(body));
        else if (!res.ok) fail(msg, "HTTP " + res.status);
      } catch (err) {
        fail(msg, err);
      }
    });
  });
