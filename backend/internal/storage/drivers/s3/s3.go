// Package s3 is a Storage Driver fronting any S3-compatible object store.
//
// Tested against AWS S3, Hetzner Object Storage (path-style endpoint,
// nbg1.your-objectstorage.com), MinIO, Backblaze B2 (S3 compat), and
// Cloudflare R2. Multipart uploads are exposed via the optional
// MultipartUploader interface for browser-direct chunked uploads.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func init() {
	storage.Register("s3", func() storage.Driver { return &Driver{} })
}

// Driver is the S3 storage driver.
type Driver struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
	prefix    string
	region    string
	endpoint  string
	pathStyle bool
	// disablePresign forces the driver to advertise no presign support
	// even though the S3 SDK can produce signed URLs. Set this when the
	// upstream object store doesn't fully implement AWS SigV4 — Hetzner
	// Object Storage / Ceph RGW reject some SDK-generated signatures
	// with `SignatureDoesNotMatch` (sweep-2026-05-09 bug 23). Falling back
	// to backend-stream is the safe path until the SigV4 quirks are
	// understood. Toggle via storage config `disable_presign: true`.
	disablePresign bool
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "s3" }

// Init configures the driver.
//
// Required: bucket, region, access_key, secret_key.
// Optional: endpoint (Hetzner: https://nbg1.your-objectstorage.com),
//
//	path_style (Hetzner needs true), prefix (storage root prefix).
func (d *Driver) Init(ctx context.Context, cfg map[string]any) error {
	d.bucket, _ = cfg["bucket"].(string)
	d.prefix, _ = cfg["prefix"].(string)
	d.region, _ = cfg["region"].(string)
	d.endpoint, _ = cfg["endpoint"].(string)
	d.pathStyle, _ = cfg["path_style"].(bool)
	d.disablePresign, _ = cfg["disable_presign"].(bool)
	// Custom endpoints almost always mean a non-AWS S3-compatible
	// service (Hetzner Object Storage, MinIO, Backblaze B2 S3-compat,
	// Cloudflare R2 — all of which serve path-style and reject
	// virtual-host-style). When the operator didn't *explicitly*
	// set `path_style`, we default to true for any custom endpoint.
	// AWS S3 itself never sets `endpoint`, so the default-false path
	// (virtual-host-style) is preserved for it. (sweep-2026-05-09 bug 23
	// — Hetzner presigned URLs returned `SignatureDoesNotMatch` because
	// the SDK signed virtual-host but Hetzner reads path-style.)
	if _, explicit := cfg["path_style"]; !explicit && d.endpoint != "" {
		d.pathStyle = true
	}
	accessKey, _ := cfg["access_key"].(string)
	secretKey, _ := cfg["secret_key"].(string)

	if d.bucket == "" {
		return errors.New("s3: config.bucket required")
	}
	if d.region == "" {
		d.region = "auto"
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(d.region),
	}
	if accessKey != "" && secretKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return fmt.Errorf("s3: aws config: %w", err)
	}

	d.client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if d.endpoint != "" {
			o.BaseEndpoint = aws.String(d.endpoint)
		}
		o.UsePathStyle = d.pathStyle
	})
	d.presigner = s3.NewPresignClient(d.client)
	return nil
}

// Capabilities — S3 supports everything except Watch (notifications go via
// SQS/EventBridge, not implemented in this skeleton).
func (d *Driver) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Read:    true,
		Write:   true,
		Move:    true,
		Copy:    true,
		Delete:  true,
		Mkdir:   true,
		Presign: !d.disablePresign,
	}
}

func (d *Driver) key(p string) string {
	clean := strings.TrimLeft(path.Clean("/"+p), "/")
	if d.prefix == "" {
		return clean
	}
	return path.Join(d.prefix, clean)
}

func (d *Driver) unkey(k string) string {
	if d.prefix != "" && strings.HasPrefix(k, d.prefix) {
		k = strings.TrimPrefix(k, d.prefix)
	}
	return "/" + strings.TrimLeft(k, "/")
}

// List implements storage.Driver.
func (d *Driver) List(ctx context.Context, p string) ([]storage.Object, error) {
	prefix := d.key(p)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	out := []storage.Object{}
	var token *string
	for {
		resp, err := d.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(d.bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("s3: list: %w", err)
		}
		for _, cp := range resp.CommonPrefixes {
			name := strings.TrimSuffix(strings.TrimPrefix(aws.ToString(cp.Prefix), prefix), "/")
			if name == "" {
				continue
			}
			out = append(out, storage.Object{
				Path: path.Join(p, name),
				Name: name,
				Kind: storage.KindDirectory,
			})
		}
		for _, obj := range resp.Contents {
			key := aws.ToString(obj.Key)
			if key == prefix {
				continue
			}
			name := strings.TrimPrefix(key, prefix)
			if strings.Contains(name, "/") {
				continue
			}
			if name == emptyMarker {
				continue // hidden empty-folder keep-marker
			}
			out = append(out, storage.Object{
				Path:  path.Join(p, name),
				Name:  name,
				Size:  aws.ToInt64(obj.Size),
				Etag:  strings.Trim(aws.ToString(obj.ETag), `"`),
				Mtime: aws.ToTime(obj.LastModified),
				Kind:  storage.KindFile,
			})
		}
		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		token = resp.NextContinuationToken
	}
	return out, nil
}

// Stat implements storage.Driver.
//
// 404 from HeadObject (NotFound / NoSuchKey) is mapped to
// storage.ErrNotFound so the manager handler can surface it as a clean
// 404 instead of a 500. Every other error keeps its original message.
func (d *Driver) Stat(ctx context.Context, p string) (storage.Object, error) {
	resp, err := d.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return storage.Object{}, storage.ErrNotFound
		}
		return storage.Object{}, fmt.Errorf("s3: head: %w", err)
	}
	return storage.Object{
		Path:  p,
		Name:  path.Base(p),
		Size:  aws.ToInt64(resp.ContentLength),
		Etag:  strings.Trim(aws.ToString(resp.ETag), `"`),
		Mtime: aws.ToTime(resp.LastModified),
		Mime:  aws.ToString(resp.ContentType),
		Kind:  storage.KindFile,
	}, nil
}

// isS3NotFound matches the various ways the SDK signals a missing key:
//   - typed errors (NotFound, NoSuchKey)
//   - smithy.APIError codes (NoSuchKey, NotFound)
//   - HTTP response error wrapping a StatusCode 404
func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	// Generic fallback for SDK error chains where the typed errors are
	// wrapped past errors.As reach (rare; some Hetzner responses).
	msg := err.Error()
	if strings.Contains(msg, "StatusCode: 404") {
		return true
	}
	if strings.Contains(msg, "NotFound") || strings.Contains(msg, "NoSuchKey") {
		return true
	}
	return false
}

// Read implements storage.Driver.
func (d *Driver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	resp, err := d.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("s3: get: %w", err)
	}
	return resp.Body, nil
}

// Write implements storage.Writer.
func (d *Driver) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	_, err := d.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
		Body:   r,
	})
	return err
}

// emptyMarker is the hidden 0-byte object filex writes inside a folder so an
// otherwise-empty directory still exists on an object store (which has no real
// directories). It is filtered from every listing (see List) and is moved /
// deleted along with its folder.
const emptyMarker = ".empty"

// Delete implements storage.Deleter. For a directory it removes every object
// under the prefix — S3 has no folders, so a single DeleteObject on the bare
// prefix key would be a no-op and orphan the contents.
func (d *Driver) Delete(ctx context.Context, p string) error {
	if d.isDir(ctx, p) {
		keys, err := d.listKeysUnder(ctx, p)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if _, err := d.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(d.bucket),
				Key:    aws.String(k),
			}); err != nil {
				return fmt.Errorf("s3: delete %s: %w", k, err)
			}
		}
		return nil
	}
	_, err := d.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
	})
	return err
}

// Move implements storage.Mover (copy + delete). A directory is moved as a
// unit by recursing the prefix: CopyObject cannot operate on a bare prefix, so
// the old single-object move 404'd on any folder (empty or not) — the S3
// folder-delete/rename bug this method fixes.
func (d *Driver) Move(ctx context.Context, src, dst string) error {
	if d.isDir(ctx, src) {
		return d.copyDir(ctx, src, dst, true)
	}
	if err := d.Copy(ctx, src, dst); err != nil {
		return err
	}
	_, err := d.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(src)),
	})
	return err
}

// Copy implements storage.Copier (server-side). Directories are copied
// recursively (same prefix rationale as Move). The CopySource header MUST be
// URL-encoded — the AWS SDK does not do it for you — or keys with spaces or
// non-ASCII characters (e.g. Turkish filenames) 404 as NoSuchKey. A genuinely
// missing source maps to storage.ErrNotFound so a delete can treat it as
// already-done rather than failing the whole batch.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	if d.isDir(ctx, src) {
		return d.copyDir(ctx, src, dst, false)
	}
	_, err := d.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(d.bucket),
		CopySource: aws.String(encodeCopySource(d.bucket, d.key(src))),
		Key:        aws.String(d.key(dst)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return storage.ErrNotFound
		}
		return err
	}
	return nil
}

// encodeCopySource URL-encodes each segment of an S3 key for the CopySource
// header (path slashes preserved). Without this, spaces and non-ASCII bytes in
// a key make CopyObject fail with NoSuchKey.
func encodeCopySource(bucket, key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return bucket + "/" + strings.Join(segs, "/")
}

// copyDir copies every object under src/ to the matching key under dst/,
// preserving the relative subtree (marker included). When del is true it also
// deletes each source object after copying — i.e. a move.
func (d *Driver) copyDir(ctx context.Context, src, dst string, del bool) error {
	srcPrefix := d.key(src)
	if !strings.HasSuffix(srcPrefix, "/") {
		srcPrefix += "/"
	}
	dstPrefix := d.key(dst)
	if !strings.HasSuffix(dstPrefix, "/") {
		dstPrefix += "/"
	}
	keys, err := d.listKeysUnder(ctx, src)
	if err != nil {
		return err
	}
	for _, k := range keys {
		dstKey := dstPrefix + strings.TrimPrefix(k, srcPrefix)
		if _, err := d.client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(d.bucket),
			CopySource: aws.String(encodeCopySource(d.bucket, k)),
			Key:        aws.String(dstKey),
		}); err != nil {
			if isS3NotFound(err) {
				continue // vanished mid-op (race) — nothing to move
			}
			return fmt.Errorf("s3: copy-dir %s: %w", k, err)
		}
		if del {
			if _, err := d.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(d.bucket),
				Key:    aws.String(k),
			}); err != nil {
				return fmt.Errorf("s3: move-dir del %s: %w", k, err)
			}
		}
	}
	return nil
}

// isDir reports whether p is a directory: no object exists at the exact key but
// ≥1 object exists under the p/ prefix. A real object at the key ⇒ file.
func (d *Driver) isDir(ctx context.Context, p string) bool {
	if _, err := d.Stat(ctx, p); err == nil {
		return false
	}
	keys, _ := d.listKeysUnder(ctx, p)
	return len(keys) > 0
}

// listKeysUnder returns every raw S3 key under p's prefix (recursive, no
// delimiter), including the folder's own marker objects. Used by the
// directory-aware Copy/Move/Delete.
func (d *Driver) listKeysUnder(ctx context.Context, p string) ([]string, error) {
	prefix := d.key(p)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	var keys []string
	var token *string
	for {
		resp, err := d.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(d.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("s3: list-under: %w", err)
		}
		for _, o := range resp.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		token = resp.NextContinuationToken
	}
	return keys, nil
}

// Mkdir writes the hidden .empty keep-marker so an empty folder exists on the
// object store (and shows as a directory) without any visible child. The
// marker is filtered from listings and moved/removed with the folder.
func (d *Driver) Mkdir(ctx context.Context, p string) error {
	_, err := d.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p) + "/" + emptyMarker),
		Body:   strings.NewReader(""),
	})
	return err
}

// PresignDownload implements storage.Presigner.
func (d *Driver) PresignDownload(ctx context.Context, p string, ttl time.Duration) (string, error) {
	req, err := d.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// PresignUpload implements storage.Presigner — returns a single PUT URL.
// For multipart, use InitMultipart instead.
func (d *Driver) PresignUpload(ctx context.Context, p string, _ int64) (storage.PresignedUpload, error) {
	req, err := d.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return storage.PresignedUpload{}, err
	}
	return storage.PresignedUpload{
		URL:       req.URL,
		Method:    req.Method,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}, nil
}

// InitMultipart implements storage.MultipartUploader.
func (d *Driver) InitMultipart(ctx context.Context, p string, _ int64, partCount int) (string, []string, error) {
	resp, err := d.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(d.bucket),
		Key:    aws.String(d.key(p)),
	})
	if err != nil {
		return "", nil, err
	}
	uploadID := aws.ToString(resp.UploadId)
	urls := make([]string, partCount)
	for i := 1; i <= partCount; i++ {
		req, err := d.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(d.bucket),
			Key:        aws.String(d.key(p)),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(int32(i)),
		}, s3.WithPresignExpires(24*time.Hour))
		if err != nil {
			return "", nil, err
		}
		urls[i-1] = req.URL
	}
	return uploadID, urls, nil
}

// CompleteMultipart implements storage.MultipartUploader.
func (d *Driver) CompleteMultipart(ctx context.Context, p, uploadID string, parts []storage.PartCompletion) error {
	completed := make([]s3types.CompletedPart, len(parts))
	for i, pp := range parts {
		completed[i] = s3types.CompletedPart{
			ETag:       aws.String(pp.Etag),
			PartNumber: aws.Int32(int32(pp.PartNumber)),
		}
	}
	_, err := d.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(d.bucket),
		Key:             aws.String(d.key(p)),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: completed},
	})
	return err
}

// AbortMultipart implements storage.MultipartUploader.
func (d *Driver) AbortMultipart(ctx context.Context, p, uploadID string) error {
	_, err := d.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(d.bucket),
		Key:      aws.String(d.key(p)),
		UploadId: aws.String(uploadID),
	})
	return err
}
