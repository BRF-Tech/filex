// A model behind OpenAI's Chat Completions API, for the AI assistant's browser
// suite (e2e 266) and its screenshots (e2e/shots/assistant.mjs) - task #269.
//
// It answers like a model that uses tools, decided by the conversation it is
// sent, so a scene needs no script: a message that asks to share calls
// file_share on the storage's first file; any other first message calls
// file_list on the storage; once a tool has answered, it writes one sentence
// about what the tool said. It streams as the API documents it (server-sent
// events: delta chunks, a finish reason, a usage chunk, [DONE]) and keeps
// every request it was sent.
//
//   const llm = await startFakeLlm({ storage: 'projects', file: 'budget-2026.xlsx' });
//   ... point the assistant at llm.url (OpenAI, base URL llm.url, model fake-model)
//   await llm.close();

import http from 'node:http';

/** What the fake says about the tool answer it was sent. */
function aboutTool(content) {
  let body;
  try {
    body = JSON.parse(content);
  } catch {
    return 'That did not work: ' + String(content).slice(0, 120);
  }
  if (Array.isArray(body.entries)) {
    const names = body.entries.map((e) => e.name).filter(Boolean);
    return names.length
      ? `This folder holds ${names.length} items: ${names.map((n) => `**${n}**`).join(', ')}.`
      : 'This folder is empty.';
  }
  const url = body.url || body.share?.url;
  if (url) return `Here is the public link: ${url}`;
  return 'Done.';
}

export function startFakeLlm({ storage, file }) {
  const requests = [];
  const server = http.createServer((req, res) => {
    if (req.url?.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ data: [{ id: 'fake-model' }, { id: 'fake-model-large' }] }));
      return;
    }
    if (!req.url?.endsWith('/chat/completions')) {
      res.writeHead(404);
      res.end();
      return;
    }
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      const body = JSON.parse(raw || '{}');
      requests.push(body);
      const msgs = body.messages ?? [];
      const last = msgs[msgs.length - 1] ?? {};
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
      const chunk = (v) => res.write(`data: ${JSON.stringify(v)}\n\n`);
      const choice = (delta, finish = null) => ({ choices: [{ delta, finish_reason: finish }] });
      const call = (name, args) => {
        chunk(choice({ tool_calls: [{ index: 0, id: `call_${requests.length}`, type: 'function', function: { name, arguments: JSON.stringify(args) } }] }));
        chunk(choice({}, 'tool_calls'));
      };
      if (last.role === 'tool') {
        for (const word of aboutTool(last.content).split(/(?<= )/)) chunk(choice({ content: word }));
        chunk(choice({}, 'stop'));
      } else if (/share|link/i.test(String(last.content))) {
        call('file_share', { path: `${storage}://${file}` });
      } else {
        call('file_list', { path: `${storage}://` });
      }
      chunk({ choices: [], usage: { prompt_tokens: 120, completion_tokens: 30 } });
      res.end('data: [DONE]\n\n');
    });
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({
        url: `http://127.0.0.1:${port}/v1`,
        requests,
        close: () => new Promise((r) => server.close(() => r())),
      });
    });
  });
}
