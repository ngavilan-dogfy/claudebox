package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// The container build context is embedded in the binary: no repo checkout
// needed at runtime, and the image tag is derived from these bytes, so any
// asset change produces a new tag and a stale image can never be reused.

//go:embed Dockerfile
var assetDockerfile []byte

//go:embed entrypoint.sh
var assetEntrypoint []byte

//go:embed init-firewall.sh
var assetFirewall []byte

func assetsHash() string {
	h := sha256.New()
	h.Write(assetDockerfile)
	h.Write(assetEntrypoint)
	h.Write(assetFirewall)
	return hex.EncodeToString(h.Sum(nil))[:12]
}
