// The S3 server `node e2e/run.mjs local --s3` starts: which image, how it is
// run, how its bucket is made. Kept out of run.mjs so a unit test can read it
// (web/tests/deploy/e2eS3Server.test.ts).
//
// ⚠ minio/minio is no longer published on Docker Hub ("pull access denied",
// measured 2026-10-01), and every `--s3` run stopped at the pull. 0.50 moved
// to Versity S3 Gateway, chosen by measurement against the driver's own
// conformance test (docs/STORAGE.md, "A local S3 server"). Its posix backend
// serves every top-level directory of its root as a bucket, so the bucket is
// a `mkdir` in the container: no extra client and no SigV4 signing here.

/** A pinned tag, never `latest`: a run measures one known server. */
export const S3_IMAGE_DEFAULT = 'versity/versitygw:v1.8.0';

/** The gateway's port inside the container. */
export const S3_CONTAINER_PORT = 7070;

/** Where the posix backend keeps its buckets, inside the container. */
export const S3_ROOT = '/data';

/**
 * The region the gateway signs with. It is part of the signature: any other
 * (filex's blank-region default is `auto`) is answered with 400
 * AuthorizationHeaderMalformed.
 */
export const S3_REGION = 'us-east-1';

/** The readiness path (`VGW_HEALTH`), answered without credentials. */
export const S3_HEALTH_PATH = '/health';

/**
 * The image to run: E2E_S3_IMAGE, else the pinned default. E2E_MINIO_IMAGE
 * named a MinIO image for the old harness; a MinIO image cannot take the
 * gateway's arguments, so it is refused with what to set instead of being
 * run and failing later.
 */
export function s3Image(env = process.env) {
  if (env.E2E_MINIO_IMAGE) {
    throw new Error(
      'E2E_MINIO_IMAGE is set, but --s3 no longer runs MinIO: it runs versitygw ' +
        `(${S3_IMAGE_DEFAULT}). Unset it; E2E_S3_IMAGE names another versitygw image (a registry mirror).`,
    );
  }
  return env.E2E_S3_IMAGE || S3_IMAGE_DEFAULT;
}

/**
 * `docker run` arguments for the server. `-v /data` is an anonymous volume:
 * the posix root exists at start and sits on a filesystem with extended
 * attributes (the backend keeps object metadata in them); removing the
 * container with `rm -v` removes it.
 */
export function s3RunArgs({ image, name, hostPort, accessKey, secretKey }) {
  return [
    'run', '-d', '--rm', '--name', name,
    '-p', `${hostPort}:${S3_CONTAINER_PORT}`,
    '-v', S3_ROOT,
    '-e', `ROOT_ACCESS_KEY=${accessKey}`,
    '-e', `ROOT_SECRET_KEY=${secretKey}`,
    '-e', 'VGW_BACKEND=posix',
    '-e', `VGW_BACKEND_ARG=${S3_ROOT}`,
    '-e', `VGW_HEALTH=${S3_HEALTH_PATH}`,
    image,
  ];
}

/** `docker exec` arguments that make a bucket: a directory in the root. */
export function s3BucketArgs(name, bucket) {
  return ['exec', name, 'mkdir', '-p', `${S3_ROOT}/${bucket}`];
}

/** `docker rm` arguments that take the anonymous volume with the container. */
export function s3RemoveArgs(name) {
  return ['rm', '-f', '-v', name];
}
