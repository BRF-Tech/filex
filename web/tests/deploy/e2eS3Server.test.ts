// The S3 server `node e2e/run.mjs local --s3` starts (e2e/lib/s3server.mjs).
//
// ⚠ 0.50: minio/minio is no longer published on Docker Hub, and every `--s3`
// run stopped at the pull ("pull access denied"). The harness moved to
// Versity S3 Gateway, and these hold the parts of that a typo would break
// without a docker run noticing until the S3 specs were already skipped or
// red: a pinned image, the bucket made under the root the gateway serves, the
// health path the harness waits on, and the region the storage signs with.
import { describe, expect, it } from 'vitest';

import {
  S3_CONTAINER_PORT,
  S3_HEALTH_PATH,
  S3_IMAGE_DEFAULT,
  S3_REGION,
  S3_ROOT,
  s3BucketArgs,
  s3Image,
  s3RemoveArgs,
  s3RunArgs,
} from '../../../e2e/lib/s3server.mjs';

/** The `-e NAME=value` pairs of a docker argv, as a map. */
function envOf(argv: string[]): Record<string, string> {
  const env: Record<string, string> = {};
  argv.forEach((a, i) => {
    if (a !== '-e') return;
    const [k, ...v] = argv[i + 1]!.split('=');
    env[k!] = v.join('=');
  });
  return env;
}

const RUN = s3RunArgs({ image: 'img:1', name: 'filex-e2e-s3-5000', hostPort: 5000, accessKey: 'ak', secretKey: 'sk' });

describe('the --s3 server', () => {
  it('is a pinned versitygw image, never latest and never MinIO', () => {
    expect(S3_IMAGE_DEFAULT).toMatch(/^versity\/versitygw:v\d+\.\d+\.\d+$/);
    expect(s3Image({})).toBe(S3_IMAGE_DEFAULT);
    expect(s3Image({ E2E_S3_IMAGE: 'mirror.example.com/versitygw:v1.8.0' })).toBe('mirror.example.com/versitygw:v1.8.0');
  });

  it('refuses E2E_MINIO_IMAGE and says what to set instead', () => {
    expect(() => s3Image({ E2E_MINIO_IMAGE: 'mirror.example.com/minio:latest' })).toThrow(/E2E_S3_IMAGE/);
  });

  it('runs the posix backend with the given keys, on the published port', () => {
    const env = envOf(RUN);
    expect(env.ROOT_ACCESS_KEY).toBe('ak');
    expect(env.ROOT_SECRET_KEY).toBe('sk');
    expect(env.VGW_BACKEND).toBe('posix');
    expect(RUN).toContain(`5000:${S3_CONTAINER_PORT}`);
    expect(RUN[RUN.length - 1]).toBe('img:1');
  });

  it('makes the bucket inside the root the gateway serves, on a volume that leaves with it', () => {
    const env = envOf(RUN);
    expect(env.VGW_BACKEND_ARG).toBe(S3_ROOT);
    expect(RUN[RUN.indexOf('-v') + 1]).toBe(S3_ROOT);
    expect(s3BucketArgs('filex-e2e-s3-5000', 'filex-e2e')).toEqual([
      'exec', 'filex-e2e-s3-5000', 'mkdir', '-p', `${S3_ROOT}/filex-e2e`,
    ]);
    expect(s3RemoveArgs('filex-e2e-s3-5000')).toEqual(['rm', '-f', '-v', 'filex-e2e-s3-5000']);
  });

  it('answers the health path the harness waits on, and signs with the region it serves', () => {
    expect(envOf(RUN).VGW_HEALTH).toBe(S3_HEALTH_PATH);
    // The region is part of the signature; the gateway's own is us-east-1
    // unless VGW_REGION says otherwise, and the harness does not set it.
    expect(envOf(RUN).VGW_REGION).toBeUndefined();
    expect(S3_REGION).toBe('us-east-1');
  });
});
