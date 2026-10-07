package lives3

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestFromEnv_NeedsTheBucketAndItsKey(t *testing.T) {
	full := map[string]string{EnvBucket: "nightly", EnvAccessKey: "key", EnvSecretKey: "secret"}
	for _, k := range []string{EnvBucket, EnvAccessKey, EnvSecretKey} {
		vars := map[string]string{}
		for kk, v := range full {
			if kk != k {
				vars[kk] = v
			}
		}
		if _, ok := FromEnv(env(vars)); ok {
			t.Fatalf("without %s the suites must skip, not run against nothing", k)
		}
	}
	cfg, ok := FromEnv(env(full))
	if !ok {
		t.Fatal("a bucket, a key and a secret name a server")
	}
	if cfg["bucket"] != "nightly" || cfg["access_key"] != "key" || cfg["secret_key"] != "secret" {
		t.Fatalf("the server is not the one named: %v", cfg)
	}
}

// Lesson #927: the region is part of the signature. Three of the four suites
// dropped it and signed for "auto", which a server with a region of its own
// refuses before any code under test runs.
func TestFromEnv_SignsForTheRegionItIsGiven(t *testing.T) {
	base := map[string]string{EnvBucket: "b", EnvAccessKey: "k", EnvSecretKey: "s", EnvEndpoint: "https://s3.eu-central-003.backblazeb2.com"}
	cfg, _ := FromEnv(env(base))
	if _, set := cfg["region"]; set {
		t.Fatalf("no region given, none passed on (the driver's own default decides): %v", cfg["region"])
	}
	base[EnvRegion] = "eu-central-003"
	cfg, _ = FromEnv(env(base))
	if cfg["region"] != "eu-central-003" {
		t.Fatalf("region = %v, want eu-central-003", cfg["region"])
	}
	if cfg["endpoint"] != "https://s3.eu-central-003.backblazeb2.com" {
		t.Fatalf("endpoint = %v", cfg["endpoint"])
	}
}

func TestFromEnv_PathStyleUnlessTurnedOff(t *testing.T) {
	base := map[string]string{EnvBucket: "b", EnvAccessKey: "k", EnvSecretKey: "s"}
	cfg, _ := FromEnv(env(base))
	if cfg["path_style"] != true {
		t.Fatalf("path style is the default: %v", cfg["path_style"])
	}
	base[EnvPathStyle] = "0"
	cfg, _ = FromEnv(env(base))
	if _, set := cfg["path_style"]; set {
		t.Fatalf("FILEX_TEST_S3_PATH_STYLE=0 asks for virtual-hosted addressing: %v", cfg["path_style"])
	}
}

// The suites read their server through this package and nowhere else: a
// copy of their own is how three of them came to sign for the wrong region.
func TestEveryLiveSuiteReadsItsServerHere(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not at %s: %v", root, err)
	}
	here, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	reads := regexp.MustCompile(`(Getenv|LookupEnv)\("FILEX_TEST_S3_`)
	var offenders []string
	suites := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if abs, _ := filepath.Abs(p); abs == here {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(b)
		if reads.MatchString(src) {
			offenders = append(offenders, filepath.ToSlash(p))
		}
		if strings.Contains(src, "lives3.Config(") {
			suites++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these read FILEX_TEST_S3_* themselves; call lives3.Config instead: %v", offenders)
	}
	// The conformance suite, the B2 reader, the rename and the driver's Init.
	if suites < 4 {
		t.Fatalf("only %d file(s) call lives3.Config - has a live suite stopped reading its server here?", suites)
	}
}
