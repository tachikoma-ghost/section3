package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Set via -ldflags at build time (see Makefile).
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

const (
	releaseManifestURL = "https://signalshell.com/releases/section3/latest.json"
	releaseBaseURL     = "https://signalshell.com/releases/section3"
	// minisign public key (key ID 4553E564F8700D47); private key in
	// ~/.config/section3/release-signing.key on the release machine.
	releasePublicKey = "RWRHDXD4ZOVTRfvfv/shVjvlkOBGp3OxN+KILl6yDWY20SByxOQP/OnO"
)

type releaseManifest struct {
	Version   int    `json:"version"`
	Published string `json:"published"`
}

// runSelf handles the "self" command namespace. These commands act on the
// section3 binary itself and are never forwarded to the daemon, so they
// cannot collide with service verbs.
func runSelf(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: section3 self <version|update>")
	}
	switch args[0] {
	case "version":
		fmt.Printf("section3 %s (commit: %s, built: %s, %s/%s)\n",
			version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
		return nil
	case "update":
		return runSelfUpdate()
	default:
		return fmt.Errorf("unknown self command: %q (usage: section3 self <version|update>)", args[0])
	}
}

func runSelfUpdate() error {
	currentVersion, _ := strconv.Atoi(version)

	fmt.Printf("Current version: %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Checking %s...\n", releaseManifestURL)

	manifest, err := fetchManifest()
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}

	if manifest.Version <= currentVersion {
		fmt.Printf("Already up to date (v%d)\n", currentVersion)
		return nil
	}

	fmt.Printf("New version available: v%d (published %s)\n", manifest.Version, manifest.Published)

	binaryName := fmt.Sprintf("section3-%s-%s", runtime.GOOS, runtime.GOARCH)
	binaryURL := fmt.Sprintf("%s/%d/%s", releaseBaseURL, manifest.Version, binaryName)
	sigURL := binaryURL + ".minisig"

	fmt.Printf("Downloading %s...\n", binaryURL)
	binaryData, err := fetchBytes(binaryURL)
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}

	sigData, err := fetchBytes(sigURL)
	if err != nil {
		return fmt.Errorf("download signature: %w", err)
	}

	if err := verifyMinisign(binaryData, sigData, releasePublicKey); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	fmt.Println("Signature verified.")

	if err := selfReplace(binaryData); err != nil {
		return fmt.Errorf("self-replace: %w", err)
	}

	fmt.Printf("Updated to v%d.\n", manifest.Version)
	fmt.Println("Note: a running daemon keeps the old version until it is restarted.")
	return nil
}

func fetchManifest() (*releaseManifest, error) {
	data, err := fetchBytes(releaseManifestURL)
	if err != nil {
		return nil, err
	}
	var m releaseManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func fetchBytes(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// verifyMinisign verifies a minisign signature against the embedded public key.
// Supports both "Ed" (raw ed25519) and "ED" (blake2b-512 prehash) algorithms.
func verifyMinisign(message, sigFile []byte, pubKeyB64 string) error {
	pubKeyRaw, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubKeyRaw) != 42 { // 2 (alg) + 8 (key ID) + 32 (pubkey)
		return fmt.Errorf("invalid public key length")
	}
	pubKey := ed25519.PublicKey(pubKeyRaw[10:]) // skip alg[2] + keyID[8]
	pubKeyID := pubKeyRaw[2:10]

	// Parse .minisig: skip untrusted comment line, decode second line
	lines := strings.Split(strings.TrimSpace(string(sigFile)), "\n")
	if len(lines) < 2 {
		return fmt.Errorf("invalid signature file")
	}
	sigRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sigRaw) != 74 { // 2 (alg) + 8 (key ID) + 64 (sig)
		return fmt.Errorf("invalid signature length")
	}

	alg := sigRaw[0:2]
	sigKeyID := sigRaw[2:10]
	sig := sigRaw[10:]

	if string(pubKeyID) != string(sigKeyID) {
		return fmt.Errorf("key ID mismatch")
	}

	var msgToVerify []byte
	switch string(alg) {
	case "Ed": // raw ed25519
		msgToVerify = message
	case "ED": // blake2b-512 prehash
		h, _ := blake2b.New512(nil)
		h.Write(message)
		msgToVerify = h.Sum(nil)
	default:
		return fmt.Errorf("unsupported algorithm: %q", alg)
	}

	if !ed25519.Verify(pubKey, msgToVerify, sig) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func selfReplace(newBinary []byte) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return err
	}

	// Checksum for confirmation
	sum := sha256.Sum256(newBinary)
	fmt.Printf("SHA256: %x\n", sum)

	// Write to temp file in same directory (same filesystem = atomic rename)
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".section3-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { os.Remove(tmpName) }()

	if _, err := tmp.Write(newBinary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	return os.Rename(tmpName, execPath)
}
