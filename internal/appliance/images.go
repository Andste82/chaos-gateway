package appliance

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cloudImageKeyring is Ubuntu's "UEC Image Automatic Signing Key <cdimage@ubuntu.com>"
// (fingerprint D2EB 4462 6FDD C30B 513D 5BB7 1A5D 6C4C 7DB8 7C81), fetched from
// keyserver.ubuntu.com and verified against the fingerprint published at
// https://ubuntu.com/docs/public-images/public-images-how-to/verify-image-checksum/ (M5b-03).
//
//go:embed testdata/ubuntu-cloudimage-keyring.gpg
var cloudImageKeyring []byte

// verifyKeyring is the keyring Fetch trusts; a package variable so tests can substitute a
// throwaway key instead of the real Ubuntu one.
var verifyKeyring = cloudImageKeyring

// Release is an Ubuntu release the appliance is tested on.
type Release struct {
	// Name is the release number, "24.04".
	Name string
	// BaseURL is the directory of the cloud image and its SHA256SUMS.
	BaseURL string
	// File is the name of the image in that directory.
	File string
}

// Releases are the releases of plan §1.5: Ubuntu 24.04 and 26.04, amd64.
var Releases = map[string]Release{
	"24.04": release("24.04"),
	"26.04": release("26.04"),
}

func release(name string) Release {
	return Release{Name: name, BaseURL: "https://cloud-images.ubuntu.com/releases/" + name + "/release/", File: "ubuntu-" + name + "-server-cloudimg-amd64.img"}
}

// ParseSums reads a SHA256SUMS file ("<hex> *<file>" or "<hex>  <file>") into a map from file name
// to hex digest.
func ParseSums(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return out
}

// Fetch returns the path of the verified image in cacheDir, downloading it first when it is missing
// or its checksum no longer matches the published one. The checksum list is always fetched: an
// image Ubuntu has replaced is downloaded again.
func Fetch(ctx context.Context, client *http.Client, cacheDir string, r Release) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	sums, err := get(ctx, client, r.BaseURL+"SHA256SUMS")
	if err != nil {
		return "", err
	}
	sig, err := get(ctx, client, r.BaseURL+"SHA256SUMS.gpg")
	if err != nil {
		return "", err
	}
	if err := verifySums(sums, sig, verifyKeyring); err != nil {
		return "", err
	}
	want, ok := ParseSums(string(sums))[r.File]
	if !ok {
		return "", fmt.Errorf("appliance: %sSHA256SUMS does not list %s", r.BaseURL, r.File)
	}
	path := filepath.Join(cacheDir, r.Name+"-"+r.File)
	if got, err := fileSum(path); err == nil && got == want {
		return path, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL+r.File, nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("appliance: download %s: %s", r.File, res.Status)
	}
	tmp, err := os.CreateTemp(cacheDir, ".download-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), res.Body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("appliance: %s has the checksum %s, the published one is %s", r.File, got, want)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("appliance: %s: %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}

// verifySums checks sig as a detached PGP signature of sums, trusting only the given keyring
// (M5b-03: SHA256SUMS was trusted over HTTPS only, with nothing to stop a compromised mirror or
// CDN from serving a different image and a matching, self-consistent checksum file).
func verifySums(sums, sig, keyring []byte) error {
	bin, err := exec.LookPath("gpgv")
	if err != nil {
		return fmt.Errorf("appliance: gpgv is not installed, cannot verify SHA256SUMS: %w", err)
	}
	dir, err := os.MkdirTemp("", "chaosgw-gpgv-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	kr, sf, gf := filepath.Join(dir, "keyring.gpg"), filepath.Join(dir, "SHA256SUMS"), filepath.Join(dir, "SHA256SUMS.gpg")
	for _, f := range []struct {
		path string
		data []byte
	}{{kr, keyring}, {sf, sums}, {gf, sig}} {
		if err := os.WriteFile(f.path, f.data, 0o600); err != nil {
			return err
		}
	}
	out, err := exec.Command(bin, "--keyring", kr, gf, sf).CombinedOutput()
	if err != nil {
		return fmt.Errorf("appliance: SHA256SUMS signature check failed: %w\n%s", err, out)
	}
	return nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
