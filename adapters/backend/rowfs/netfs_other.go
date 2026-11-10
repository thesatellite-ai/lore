//go:build !darwin && !linux

package rowfs

// networkFSType has no portable probe on this OS; the cloud-sync path rule
// in CheckLocal still applies.
func networkFSType(string) string { return "" }
