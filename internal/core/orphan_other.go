//go:build !linux

package core

func CleanupOrphanCloudflared(cfg *Config) []string {
	return nil
}
