// Package lives3 tells the suites that talk to a REAL S3 server which one:
// the FILEX_TEST_S3_* variables, read in one place.
//
//	FILEX_TEST_S3_ENDPOINT    the server (empty: AWS itself)
//	FILEX_TEST_S3_BUCKET      a bucket the suites may write to and delete from
//	FILEX_TEST_S3_ACCESS_KEY  its key ...
//	FILEX_TEST_S3_SECRET_KEY  ... and secret
//	FILEX_TEST_S3_REGION      the region the requests are signed for
//	FILEX_TEST_S3_PATH_STYLE  "0" for virtual-hosted addressing (default: path style)
//
// Without a bucket, a key and a secret every suite skips itself, so `go test
// ./...` stays green on a machine that has no server. The build host's nightly
// run sets them and treats a skip as red (scripts/chain/job/s3-live.sh).
//
// ⚠ Why one place. Four suites read these variables (the provider
// conformance, the B2 report reader, issue #21's rename, the driver's Init),
// each with its own copy, and only one of the copies read the region: the
// other three always signed for "auto", which a server with a region of its
// own refuses - Versity S3 Gateway with 400 AuthorizationHeaderMalformed
// (lesson #927, measured 2026-10-02). Against such a server three of the four
// could not pass whatever the code did. TestEveryLiveSuiteReadsItsServerHere
// keeps a fifth copy from appearing.
package lives3

import (
	"os"
	"testing"
)

// The variables, as a person sets them.
const (
	EnvEndpoint  = "FILEX_TEST_S3_ENDPOINT"
	EnvBucket    = "FILEX_TEST_S3_BUCKET"
	EnvAccessKey = "FILEX_TEST_S3_ACCESS_KEY"
	EnvSecretKey = "FILEX_TEST_S3_SECRET_KEY"
	EnvRegion    = "FILEX_TEST_S3_REGION"
	EnvPathStyle = "FILEX_TEST_S3_PATH_STYLE"
)

// FromEnv is the s3 driver's configuration for the server getenv names, and
// false when the bucket, the key or the secret is not set. The region is
// passed on when set (the driver signs for "auto" without one); addressing is
// path style unless FILEX_TEST_S3_PATH_STYLE is "0" - what a local MinIO,
// Garage or Versity needs, and what B2 and AWS accept.
func FromEnv(getenv func(string) string) (map[string]any, bool) {
	bucket, access, secret := getenv(EnvBucket), getenv(EnvAccessKey), getenv(EnvSecretKey)
	if bucket == "" || access == "" || secret == "" {
		return nil, false
	}
	cfg := map[string]any{
		"bucket":     bucket,
		"endpoint":   getenv(EnvEndpoint),
		"access_key": access,
		"secret_key": secret,
	}
	if r := getenv(EnvRegion); r != "" {
		cfg["region"] = r
	}
	if getenv(EnvPathStyle) != "0" {
		cfg["path_style"] = true
	}
	return cfg, true
}

// Config is FromEnv over this process's environment, and skips t when the
// server is not named: "set FILEX_TEST_S3_* to <what>".
func Config(t testing.TB, what string) map[string]any {
	t.Helper()
	cfg, ok := FromEnv(os.Getenv)
	if !ok {
		t.Skip("set FILEX_TEST_S3_* to " + what)
	}
	return cfg
}
