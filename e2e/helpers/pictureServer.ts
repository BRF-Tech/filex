/**
 * pictureServer - "another server" for the specs that measure what a preview
 * asks of one (274, 275): a plain HTTP server on its own port, so another
 * origin than filex's, that answers every path with a 1x1 PNG (a .css path
 * with an empty style sheet) and records each request with the Referer it
 * carried.
 */
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';

/** A 1x1 transparent PNG. */
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=', 'base64');

export interface PictureServer {
  /** `http://127.0.0.1:<port>`. */
  origin: string;
  /** Every request, in order: its path and query, and its Referer. */
  asked: Array<{ url: string; referer: string | undefined }>;
  /** How many times `/<name>?t=<tag>` was asked for. */
  hits(name: string, tag: string): number;
  /** The requests whose query ends in `?t=<tag>`. */
  tagged(tag: string): Array<{ url: string; referer: string | undefined }>;
  close(): Promise<void>;
}

export async function startPictureServer(): Promise<PictureServer> {
  const asked: PictureServer['asked'] = [];
  const server = createServer((req, res) => {
    asked.push({ url: req.url ?? '', referer: req.headers.referer });
    const css = (req.url ?? '').split('?')[0].endsWith('.css');
    res.writeHead(200, {
      'Content-Type': css ? 'text/css' : 'image/png',
      'Cache-Control': 'no-store',
      'Access-Control-Allow-Origin': '*',
    });
    res.end(css ? 'p { color: inherit; }' : PNG);
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  return {
    origin: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    asked,
    hits: (name, tag) => asked.filter((a) => a.url === `/${name}?t=${tag}`).length,
    tagged: (tag) => asked.filter((a) => a.url.endsWith(`?t=${tag}`)),
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}
